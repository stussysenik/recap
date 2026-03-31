package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

	// Discover Kitty panes via socket protocol (richest data source)
	kittyPanes, kittySock := listKittyPanes()
	if len(kittyPanes) > 0 {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d kitty pane(s) via socket\n", len(kittyPanes))
	}

	// When Kitty panes are found via socket, filter out the opaque Kitty window
	// from the window list — the socket gives us richer per-pane data.
	if len(kittyPanes) > 0 {
		var filtered []WindowInfo
		for _, w := range windows {
			if strings.Contains(strings.ToLower(w.Owner), "kitty") {
				continue
			}
			filtered = append(filtered, w)
		}
		windows = filtered
	}

	// Discover shell processes via libproc (system-level process tree)
	shells, _ := listShellProcesses()
	if len(shells) > 0 {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d shell process(es) via libproc\n", len(shells))
	}

	// Load shell session registry (optional CWD/command enrichment)
	activeSessions := listActiveSessions()
	if len(activeSessions) > 0 {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d tracked shell session(s)\n", len(activeSessions))
	}

	// Detect terminal tabs via AXTabGroup for non-Ghostty, non-Kitty terminals
	var tabItems []TabItem
	for _, w := range windows {
		if w.Type != AppTerminal {
			continue
		}
		if isGhostty(w.Owner) {
			continue // Ghostty has its own pane detection
		}
		if strings.Contains(strings.ToLower(w.Owner), "kitty") {
			continue // Kitty uses socket protocol
		}
		tabs, _ := detectTabs(w)
		if len(tabs) > 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d tab(s) in %s\n", len(tabs), w.Owner)
			for _, tab := range tabs {
				tabItems = append(tabItems, TabItem{
					ParentWindow: w,
					AXTab:        tab,
				})
			}
		}
	}

	// Build dedup engine: filter shells already accounted for by tmux/cmux/kitty
	var orphanShells []ShellProc
	if len(shells) > 0 {
		// Correlate shells to windows via PPID chain
		correlated := correlateShellsToWindows(shells, windows)
		orphans := shellsWithoutWindows(shells, correlated)

		// Filter out shells whose PPID matches a tmux server
		// (they're already in the tmux pane list)
		tmuxServerPIDs := findTmuxServerPIDs()

		// Filter out shells managed by Kitty
		kittyPIDs := make(map[int]bool)
		for _, kp := range kittyPanes {
			kittyPIDs[kp.PID] = true
		}

		for _, s := range orphans {
			// Skip tmux-managed shells
			if tmuxServerPIDs[s.PPID] {
				continue
			}
			// Skip Kitty-managed shells
			if kittyPIDs[s.PID] {
				continue
			}
			orphanShells = append(orphanShells, s)
		}
	}

	// --- Build unified detect items with dedup rules ---
	var items []DetectItem

	// Group: cmux surfaces (highest priority for cmux windows)
	for i := range cmuxSurfaces {
		items = append(items, DetectItem{Cmux: &cmuxSurfaces[i]})
	}

	// Group: Kitty panes (replace opaque Kitty window with per-pane items)
	for i := range kittyPanes {
		items = append(items, DetectItem{Kitty: &KittyPaneItem{
			Pane:       kittyPanes[i],
			SocketPath: kittySock,
		}})
	}

	// Group: Terminal tabs (replace window with individual tab items)
	// Track which window PIDs have been decomposed into tabs
	windowsWithTabs := make(map[int]bool)
	for i := range tabItems {
		windowsWithTabs[tabItems[i].ParentWindow.PID] = true
		items = append(items, DetectItem{Tab: &tabItems[i]})
	}

	// Group: Windows (skip those already decomposed into tabs)
	for i := range windows {
		if windowsWithTabs[windows[i].PID] {
			continue
		}
		items = append(items, DetectItem{Window: &windows[i]})
	}

	// Group: tmux panes
	for i := range tmuxPanes {
		items = append(items, DetectItem{Tmux: &tmuxPanes[i]})
	}

	// Group: Orphan TTY sessions (shells not in any window/tmux/kitty)
	for i := range orphanShells {
		sess := findShellSessionForPID(orphanShells[i].PID, activeSessions)
		items = append(items, DetectItem{TTYShell: &TTYShellItem{
			Shell:   orphanShells[i],
			Session: sess,
		}})
	}

	if len(items) == 0 {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m No windows, panes, or sessions found.\n")
		os.Exit(1)
	}

	// Debug list mode
	if hasFlag("--list") {
		renderDetectList(windows, tmuxPanes, cmuxSurfaces)
		// Also list new sources
		if len(kittyPanes) > 0 {
			fmt.Printf("  \033[1;93mKitty Panes:\033[0m\n")
			for _, kp := range kittyPanes {
				title := kp.Title
				if title == "" {
					title = kp.ForegroundProcess()
				}
				fmt.Printf("    \033[36m[text]\033[0m kitty: %s \033[90m(%dx%d, PID %d)\033[0m\n",
					title, kp.Columns, kp.Lines, kp.PID)
			}
			fmt.Println()
		}
		if len(tabItems) > 0 {
			fmt.Printf("  \033[1;93mTerminal Tabs:\033[0m\n")
			for _, t := range tabItems {
				active := ""
				if t.AXTab.Active {
					active = " \033[32m*active\033[0m"
				}
				fmt.Printf("    \033[33m[screenshot]\033[0m %s — tab %d: %s%s\n",
					strings.ToLower(t.ParentWindow.Owner), t.AXTab.Tab+1, t.AXTab.Title, active)
			}
			fmt.Println()
		}
		if len(orphanShells) > 0 {
			fmt.Printf("  \033[1;93mTTY Sessions:\033[0m\n")
			for _, s := range orphanShells {
				sess := findShellSessionForPID(s.PID, activeSessions)
				extra := ""
				if sess != nil && sess.CWD != "" {
					cwd := sess.CWD
					if home, err := os.UserHomeDir(); err == nil {
						cwd = strings.Replace(cwd, home, "~", 1)
					}
					extra = fmt.Sprintf(" — %s", cwd)
				}
				fmt.Printf("    \033[90m[tty]\033[0m %s (PID %d, %s)%s\n",
					s.Comm, s.PID, s.TTY, extra)
			}
			fmt.Println()
		}
		return
	}

	// Select items
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
				paths, err = captureAndRenderCmux(*d.Cmux, format, outputPath)
			} else if d.Tmux != nil {
				paths, err = captureAndRenderTmux(*d.Tmux, format, outputPath)
			} else if d.Kitty != nil {
				paths, err = captureAndRenderKitty(*d.Kitty, format, outputPath)
			} else if d.Tab != nil {
				// Tab capture: use parent window's adapter
				paths, err = captureAndRender(d.Tab.ParentWindow, format, outputPath, nil)
			} else if d.TTYShell != nil {
				// TTY shell: if we have a shell session with data, render it
				// Otherwise skip — we can't capture a TTY we don't control
				if d.TTYShell.Session != nil {
					paths, err = captureAndRenderTTY(*d.TTYShell, format, outputPath)
				} else {
					err = fmt.Errorf("no shell hook data for %s (PID %d) — install with: eval \"$(recap shell-init %s)\"",
						d.TTYShell.Shell.Comm, d.TTYShell.Shell.PID, d.TTYShell.Shell.Comm)
				}
			} else if d.Window != nil {
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

// captureAndRenderKitty runs the full pipeline for a Kitty pane:
// socket get-text -> ANSI -> HTML -> PDF/PNG.
func captureAndRenderKitty(pane KittyPaneItem, format, outputBase string) ([]string, error) {
	adapter := &KittyAdapter{}
	result, err := adapter.CaptureKittyPane(pane)
	if err != nil {
		return nil, err
	}

	path, err := renderSingle(result, format, outputBase, "")
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

// captureAndRenderTTY renders a TTY shell session using shell hook data.
// This is a best-effort capture — we render the session metadata and any
// last command info as plain text content.
func captureAndRenderTTY(shell TTYShellItem, format, outputBase string) ([]string, error) {
	sess := shell.Session
	if sess == nil {
		return nil, fmt.Errorf("no shell session data")
	}

	// Build a text representation of the shell session
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Shell Session: %s (PID %d)\n", shell.Shell.Comm, shell.Shell.PID))
	sb.WriteString(fmt.Sprintf("TTY: %s\n", shell.Shell.TTY))
	sb.WriteString(fmt.Sprintf("CWD: %s\n", sess.CWD))
	if sess.LastCmd != "" {
		sb.WriteString(fmt.Sprintf("Last Command: %s\n", sess.LastCmd))
	}
	sb.WriteString(fmt.Sprintf("Updated: %s\n", sess.UpdatedAt.Format(time.RFC3339)))

	win := WindowInfo{
		Owner:    shell.Shell.Comm,
		Name:     fmt.Sprintf("TTY %s — PID %d", shell.Shell.TTY, shell.Shell.PID),
		OnScreen: true,
	}

	result := &CaptureResult{
		Window:      win,
		ContentType: ContentTextPlain,
		Data:        []byte(sb.String()),
		Title:       fmt.Sprintf("%s — %s", shell.Shell.Comm, sess.CWD),
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
		suffix := ""
		if len(result.Panes) > 1 {
			suffix = fmt.Sprintf("-pane%d", pane.Index+1)
		}

		if pane.ContentType == ContentMultiImage && len(pane.Images) > 0 && format == "png" {
			// Fast path: stitch screenshots directly — no Chrome needed.
			outPath := outputBase
			if outPath == "" {
				home, _ := os.UserHomeDir()
				ts := time.Now().Format("2006-01-02_15-04-05")
				safeName := sanitizeFilename(result.Window.Owner)
				outPath = filepath.Join(home, "Desktop",
					fmt.Sprintf("recap-%s-%s%s.png", safeName, ts, suffix))
			} else if suffix != "" {
				ext := filepath.Ext(outPath)
				base := strings.TrimSuffix(outPath, ext)
				outPath = base + suffix + ext
			}
			if err := stitchImagesPNG(pane.Images, outPath); err != nil {
				fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d stitch failed: %v\n", pane.Index+1, err)
				continue
			}
			paths = append(paths, outPath)
			continue
		}

		var htmlStr string
		if pane.ContentType == ContentMultiImage && len(pane.Images) > 0 {
			// Multi-image scroll-stitch PDF: use Chrome renderer
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

// findTmuxServerPIDs returns a set of PIDs that are tmux server processes.
// Used to filter shells whose PPID is a tmux server (they're already in tmux list).
func findTmuxServerPIDs() map[int]bool {
	result := make(map[int]bool)
	if !isTmuxAvailable() {
		return result
	}
	// tmux display-message -p '#{pid}' gives the server PID
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pid}").Output()
	if err == nil {
		pid := atoi(strings.TrimSpace(string(out)))
		if pid > 0 {
			result[pid] = true
		}
	}
	return result
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
