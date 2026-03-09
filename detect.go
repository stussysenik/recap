package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func cmdDetect() {
	format := "pdf"
	if hasFlag("--png") {
		format = "png"
	}
	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}

	// Detect all visible windows
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Detecting windows...\n")

	windows, err := listWindows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Check that Screen Recording permission is granted:\n")
		fmt.Fprintf(os.Stderr, "        System Settings \u2192 Privacy & Security \u2192 Screen Recording\n")
		os.Exit(1)
	}

	if len(windows) == 0 {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m No visible windows found.\n")
		fmt.Fprintf(os.Stderr, "        Are your windows visible (not minimized)?\n")
		os.Exit(1)
	}

	// Debug list mode
	if hasFlag("--list") {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d windows:\n", len(windows))
		renderWindowList(windows)
		return
	}

	// Select windows
	var selected []WindowInfo
	if hasFlag("--all") || hasFlag("-a") {
		for _, w := range windows {
			if w.Type == AppTerminal {
				selected = append(selected, w)
			}
		}
	} else {
		selected = runSelector(windows)
	}
	if len(selected) == 0 {
		if hasFlag("--all") || hasFlag("-a") {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m No terminal windows found.\n")
			if others := summarizeWindowTypes(windows); others != "" {
				fmt.Fprintf(os.Stderr, "        Detected non-terminal windows: %s\n", others)
			}
			fmt.Fprintf(os.Stderr, "        Use \033[1mrecap detect --list\033[0m to see all visible windows.\n")
			fmt.Fprintf(os.Stderr, "        Note: windows on other Spaces or minimized are not visible.\n")
		} else {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m No windows selected\n")
		}
		return
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Capturing %d window(s)...\n", len(selected))

	// For Ghostty windows with split panes, show pane selector
	paneSelections := make(map[int][]int) // windowIdx in selected -> selected pane indices
	isAllMode := hasFlag("--all") || hasFlag("-a")
	if !isAllMode {
		for i, w := range selected {
			if isGhostty(w.Owner) {
				panes, _ := detectGhosttyPanes(w)
				if len(panes) > 1 {
					fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d panes in %s\n", len(panes), w.Label())
					selectedPanes := runPaneSelector(w, panes)
					if selectedPanes == nil {
						fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane selection cancelled for %s\n", w.Label())
						continue
					}
					var indices []int
					for _, p := range selectedPanes {
						indices = append(indices, p.Index)
					}
					paneSelections[i] = indices
				}
			}
		}
	} else {
		for _, w := range selected {
			if isGhostty(w.Owner) {
				if n := countGhosttyPanes(w); n > 1 {
					fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d panes in %s (capturing all)\n", n, w.Label())
				}
			}
		}
	}

	// Concurrent capture+render pipeline with semaphore
	sem := make(chan struct{}, 3) // limit concurrent chromedp instances
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []string
	var errors []string

	for i, w := range selected {
		wg.Add(1)
		selectedPaneIndices := paneSelections[i]
		go func(win WindowInfo, paneIndices []int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			paths, err := captureAndRender(win, format, outputPath, paneIndices)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", win.Owner, err))
			} else {
				results = append(results, paths...)
			}
		}(w, selectedPaneIndices)
	}

	wg.Wait()

	// Report errors
	for _, e := range errors {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %s\n", e)
	}

	// Report successes and open files
	for _, path := range results {
		fmt.Fprintf(os.Stderr, "\033[32m\u2713\033[0m %s\n", path)
		openFile(path)
	}

	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m all captures failed\n")
		os.Exit(1)
	}
}

// captureAndRender runs the full pipeline for a single window:
// capture → build HTML → render to PDF/PNG.
// Returns multiple paths when a window has split panes.
// paneIndices controls which panes to capture for Ghostty (nil = all).
func captureAndRender(w WindowInfo, format, outputBase string, paneIndices []int) ([]string, error) {
	// 1. Capture
	adapter := adapterFor(w)

	// Pass pane selection to GhosttyAdapter
	if ga, ok := adapter.(*GhosttyAdapter); ok && len(paneIndices) > 0 {
		ga.SelectedPanes = paneIndices
	}

	result, err := adapter.Capture(w)
	if err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}

	// Single-pane path
	if len(result.Panes) == 0 {
		path, err := renderSingle(result, format, outputBase, "")
		if err != nil {
			return nil, err
		}
		return []string{path}, nil
	}

	// Multi-pane: one output per pane
	var paths []string
	for _, pane := range result.Panes {
		var htmlStr string

		if pane.ContentType == ContentMultiImage && len(pane.Images) > 0 {
			// Multi-image scroll-stitch: use dedicated renderer
			htmlStr, err = buildMultiImageHTML(pane.Title, pane.Images, pane.SearchText, result.Window)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d render failed: %v\n", pane.Index+1, err)
				continue
			}
		} else {
			// Single screenshot: use standard renderer
			single := &CaptureResult{
				Window:      result.Window,
				ContentType: pane.ContentType,
				Data:        pane.Data,
				SearchText:  pane.SearchText,
				Title:       pane.Title,
			}
			htmlStr, err = buildCaptureHTML(single)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d render failed: %v\n", pane.Index+1, err)
				continue
			}
		}

		suffix := ""
		if len(result.Panes) > 1 {
			suffix = fmt.Sprintf("-pane%d", pane.Index+1)
		}
		path, err := renderFromHTML(htmlStr, format, outputBase, suffix, result.Window)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d render failed: %v\n", pane.Index+1, err)
			continue
		}
		paths = append(paths, path)
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("all pane renders failed")
	}
	return paths, nil
}

// renderFromHTML renders pre-built HTML to a file.
func renderFromHTML(htmlStr, format, outputBase, suffix string, w WindowInfo) (string, error) {
	outPath := outputBase
	if outPath == "" {
		home, _ := os.UserHomeDir()
		ts := time.Now().Format("2006-01-02_15-04-05")
		safeName := sanitizeFilename(w.Owner)
		outPath = filepath.Join(home, "Desktop",
			fmt.Sprintf("recap-%s-%s%s.%s", safeName, ts, suffix, format))
	} else if suffix != "" {
		ext := filepath.Ext(outPath)
		base := strings.TrimSuffix(outPath, ext)
		outPath = base + suffix + ext
	}

	var err error
	if format == "png" {
		err = renderHTMLtoPNG(htmlStr, outPath)
	} else {
		err = renderHTMLtoPDF(htmlStr, outPath)
	}
	if err != nil {
		return "", fmt.Errorf("render: %w", err)
	}
	return outPath, nil
}

// renderSingle renders a single CaptureResult to a file.
// suffix is appended before the extension (e.g. "-pane1").
func renderSingle(result *CaptureResult, format, outputBase, suffix string) (string, error) {
	// Build HTML
	htmlStr, err := buildCaptureHTML(result)
	if err != nil {
		return "", fmt.Errorf("html: %w", err)
	}

	// Determine output path
	outPath := outputBase
	if outPath == "" {
		home, _ := os.UserHomeDir()
		ts := time.Now().Format("2006-01-02_15-04-05")
		safeName := sanitizeFilename(result.Window.Owner)
		outPath = filepath.Join(home, "Desktop",
			fmt.Sprintf("recap-%s-%s%s.%s", safeName, ts, suffix, format))
	} else if suffix != "" {
		// Insert suffix before extension in explicit output path
		ext := filepath.Ext(outPath)
		base := strings.TrimSuffix(outPath, ext)
		outPath = base + suffix + ext
	}

	// Render
	if format == "png" {
		err = renderHTMLtoPNG(htmlStr, outPath)
	} else {
		err = renderHTMLtoPDF(htmlStr, outPath)
	}
	if err != nil {
		return "", fmt.Errorf("render: %w", err)
	}

	return outPath, nil
}

// summarizeWindowTypes returns a comma-separated list of unique non-terminal
// window owner names from the window list. Returns "" if none.
func summarizeWindowTypes(windows []WindowInfo) string {
	seen := make(map[string]bool)
	var names []string
	for _, w := range windows {
		if w.Type != AppTerminal && !seen[w.Owner] {
			seen[w.Owner] = true
			names = append(names, w.Owner)
		}
	}
	return strings.Join(names, ", ")
}

// sanitizeFilename makes a string safe for use in filenames.
func sanitizeFilename(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	// Collapse multiple dashes
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}
