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

	// Detect all windows (across all Spaces, minimized, fullscreen)
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Detecting windows...\n")

	windows, err := listWindows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Check that Screen Recording permission is granted:\n")
		fmt.Fprintf(os.Stderr, "        System Settings \u2192 Privacy & Security \u2192 Screen Recording\n")
		os.Exit(1)
	}

	// Discover tmux panes (auto-detect if tmux server is running)
	tmuxPanes := listTmuxPanes()
	if len(tmuxPanes) > 0 {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d tmux pane(s)\n", len(tmuxPanes))
	}

	// Discover cmux surfaces (auto-detect via socket)
	cmuxSurfaces := listCmuxSurfaces()
	if len(cmuxSurfaces) > 0 {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d cmux surface(s) across %d workspace(s)\n",
			len(cmuxSurfaces), countCmuxWorkspaces(cmuxSurfaces))
	}

	// When cmux surfaces are found, filter out the opaque cmux window from
	// the window list — individual surfaces replace it with richer access.
	if len(cmuxSurfaces) > 0 {
		var filtered []WindowInfo
		for _, w := range windows {
			if strings.Contains(strings.ToLower(w.Owner), "cmux") {
				continue
			}
			filtered = append(filtered, w)
		}
		windows = filtered
	}

	// Build unified detect items
	var items []DetectItem
	for i := range cmuxSurfaces {
		items = append(items, DetectItem{Cmux: &cmuxSurfaces[i]})
	}
	for i := range windows {
		items = append(items, DetectItem{Window: &windows[i]})
	}
	for i := range tmuxPanes {
		items = append(items, DetectItem{Tmux: &tmuxPanes[i]})
	}

	if len(items) == 0 {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m No windows or tmux panes found.\n")
		os.Exit(1)
	}

	// Debug list mode
	if hasFlag("--list") {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d windows, %d tmux panes, %d cmux surfaces:\n",
			len(windows), len(tmuxPanes), len(cmuxSurfaces))
		renderDetectList(windows, tmuxPanes, cmuxSurfaces)
		return
	}

	// Select items (windows + tmux panes)
	var selected []DetectItem
	if hasFlag("--all") || hasFlag("-a") {
		selected = items
	} else {
		// Find active item for pre-selection
		activeIdx := findActiveItem(items)
		if activeIdx >= 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Pre-selected: %s\n",
				items[activeIdx].Label())
		}
		selected = runDetectSelector(items, activeIdx)
	}
	if len(selected) == 0 {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m No items selected\n")
		return
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Capturing %d item(s)...\n", len(selected))

	// For Ghostty windows with split panes, show pane selector
	// Skip cmux windows — they're decomposed into individual surfaces
	paneSelections := make(map[int][]int) // index in selected -> selected pane indices
	for i, item := range selected {
		if item.Window != nil && isGhostty(item.Window.Owner) && !strings.Contains(strings.ToLower(item.Window.Owner), "cmux") {
			panes, _ := detectGhosttyPanes(*item.Window)
			if len(panes) > 1 {
				fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d panes in %s\n", len(panes), item.Window.Label())
				selectedPanes := runPaneSelector(*item.Window, panes, -1) // No pre-selection for panes yet
				if selectedPanes == nil {
					fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane selection cancelled for %s\n", item.Window.Label())
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

	// Concurrent capture+render pipeline with semaphore
	sem := make(chan struct{}, 3) // limit concurrent chromedp instances
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []string
	var errors []string

	for i, item := range selected {
		wg.Add(1)
		selectedPaneIndices := paneSelections[i]
		go func(d DetectItem, paneIndices []int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var paths []string
			var err error

			if d.Cmux != nil {
				// cmux surface: text-based capture via cmux read-screen
				paths, err = captureAndRenderCmux(*d.Cmux, format, outputPath)
			} else if d.Tmux != nil {
				// tmux pane: text-based capture via tmux capture-pane
				paths, err = captureAndRenderTmux(*d.Tmux, format, outputPath)
			} else {
				// Window: screenshot-based capture
				paths, err = captureAndRender(*d.Window, format, outputPath, paneIndices)
			}

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", d.Label(), err))
			} else {
				results = append(results, paths...)
			}
		}(item, selectedPaneIndices)
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

// captureAndRenderTmux runs the full pipeline for a tmux pane:
// tmux capture-pane -> ANSI -> HTML -> PDF/PNG.
func captureAndRenderTmux(pane TmuxPane, format, outputBase string) ([]string, error) {
	adapter := &TmuxAdapter{}
	result, err := adapter.CapturePane(pane)
	if err != nil {
		return nil, err
	}

	path, err := renderSingle(result, format, outputBase, "")
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

// captureAndRenderCmux runs the full pipeline for a cmux surface:
// cmux read-screen -> plain text -> HTML -> PDF/PNG.
func captureAndRenderCmux(surface CmuxSurface, format, outputBase string) ([]string, error) {
	adapter := &CmuxAdapter{}
	result, err := adapter.CaptureSurface(surface)
	if err != nil {
		return nil, err
	}

	path, err := renderSingle(result, format, outputBase, "")
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

// captureAndRender runs the full pipeline for a single window:
// capture -> build HTML -> render to PDF/PNG.
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

// findActiveItem returns the index of the item that should be pre-selected.
// Returns -1 if no active item is detected.
func findActiveItem(items []DetectItem) int {
	// Priority 1: cmux surface where recap is running (Here flag)
	for i, d := range items {
		if d.Cmux != nil && d.Cmux.Here {
			return i
		}
	}

	// Priority 2: active cmux surface
	for i, d := range items {
		if d.Cmux != nil && d.Cmux.Active {
			return i
		}
	}

	// Priority 3: active tmux pane
	for i, d := range items {
		if d.Tmux != nil && d.Tmux.Active {
			return i
		}
	}

	// Priority 4: frontmost window
	for i, d := range items {
		if d.Window != nil && d.Window.IsActive {
			return i
		}
	}

	// Priority 5: first on-screen terminal window
	for i, d := range items {
		if d.Window != nil && d.Window.OnScreen && d.Window.Type == AppTerminal {
			return i
		}
	}

	return -1 // no active item found
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
