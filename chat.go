package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// cmdChat is a quick Ghostty capture with precise window/pane targeting.
// Flags: --title PATTERN, --window-id N, --pane N, --active, --active-pane,
// --tab NAME, --tab-list, --png, --output PATH
func cmdChat() {
	format := "pdf"
	if hasFlag("--png") {
		format = "png"
	}
	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}
	titleFilter := strings.TrimSpace(getFlag("--title"))

	// --tab-list: print all Ghostty tabs and exit
	if hasFlag("--tab-list") {
		tabs, err := listGhosttyTabs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
			os.Exit(1)
		}
		if len(tabs) == 0 {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no Ghostty tabs found\n")
			os.Exit(1)
		}
		currentName := currentGhosttyTabName()
		for i, tab := range tabs {
			marker := "  "
			if stripSpinner(currentName) == tab.Name || currentName == tab.Name {
				marker = "→ "
			}
			fmt.Fprintf(os.Stderr, "%s%d) %s\n", marker, i+1, tab.Name)
		}
		os.Exit(0)
	}

	// --tab NAME: switch to a specific tab before capturing
	var tabSwitchBack func()
	if tabFilter := getFlag("--tab"); tabFilter != "" {
		tabs, err := listGhosttyTabs()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
			os.Exit(1)
		}

		// Find matching tab by substring (case-insensitive)
		var target *GhosttyTab
		for i := range tabs {
			if strings.Contains(strings.ToLower(tabs[i].Name), strings.ToLower(tabFilter)) {
				target = &tabs[i]
				break
			}
		}
		if target == nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no tab matching %q\n", tabFilter)
			fmt.Fprintf(os.Stderr, "  Available tabs:\n")
			for i, tab := range tabs {
				fmt.Fprintf(os.Stderr, "    %d) %s\n", i+1, tab.Name)
			}
			os.Exit(1)
		}

		// Find current tab so we can switch back
		currentName := currentGhosttyTabName()
		var currentTab *GhosttyTab
		for i := range tabs {
			if stripSpinner(currentName) == tabs[i].Name || currentName == tabs[i].Name {
				currentTab = &tabs[i]
				break
			}
		}

		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Switching to tab: %s\n", target.Name)
		if err := switchGhosttyTab(target.MenuIndex); err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
			os.Exit(1)
		}
		time.Sleep(500 * time.Millisecond)

		// Set up switch-back for when we're done
		if currentTab != nil && currentTab.MenuIndex != target.MenuIndex {
			savedIndex := currentTab.MenuIndex
			tabSwitchBack = func() {
				fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Switching back to: %s\n", currentTab.Name)
				switchGhosttyTab(savedIndex)
			}
		}
	}

	// Defer tab switch-back to run after capture completes
	if tabSwitchBack != nil {
		defer tabSwitchBack()
	}

	// Headless PDF/text-first fast paths. When a specific Ghostty terminal can be
	// resolved directly, capture it without activating the app or simulating input.
	if getFlag("--window-id") == "" && getFlag("--pane") == "" {
		if hasFlag("--active-pane") {
			term, windowTitle, err := frontGhosttyFocusedTerminal()
			if err == nil && term != nil {
				fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Headless capture: focused Ghostty terminal %q\n", ghosttyTerminalDisplayName(*term))
				paths, err := captureAndRenderGhosttyTerminal(*term, windowTitle, format, outputPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
					os.Exit(1)
				}
				for _, path := range paths {
					fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", path)
					openFile(path)
				}
				return
			}
		}

		if titleFilter != "" {
			terminals, windowTitle, err := listGhosttySelectedTabTerminals()
			if err == nil {
				matches := findGhosttyTerminalMatches(terminals, titleFilter)
				switch len(matches) {
				case 1:
					fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Headless capture: Ghostty terminal %q\n", ghosttyTerminalDisplayName(matches[0]))
					paths, err := captureAndRenderGhosttyTerminal(matches[0], windowTitle, format, outputPath)
					if err != nil {
						fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
						os.Exit(1)
					}
					for _, path := range paths {
						fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", path)
						openFile(path)
					}
					return
				case 0:
					// Fall through to window-title matching below.
				default:
					fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m multiple Ghostty terminals matched --title %q\n", titleFilter)
					for _, term := range matches {
						fmt.Fprintf(os.Stderr, "  [%s] %s\n", term.ID, ghosttyTerminalDisplayName(term))
					}
					fmt.Fprintf(os.Stderr, "  Narrow the title or use --active-pane after switching focus inside Ghostty.\n")
					os.Exit(1)
				}
			}
		}
	}

	// Fast path: when --window-id is provided, capture directly via screencapture -l
	// without needing listWindows() (which requires CGWindowListCopyWindowInfo permission).
	if widStr := getFlag("--window-id"); widStr != "" {
		wid, err := strconv.Atoi(widStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m --window-id must be an integer (got %q)\n", widStr)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Capturing window %d...\n", wid)

		// Try full pipeline first (listWindows → scroll-stitch), fall back to direct screenshot
		windows, listErr := listWindows()
		var ghosttyWin *WindowInfo
		if listErr == nil {
			for i := range windows {
				if windows[i].ID == wid {
					ghosttyWin = &windows[i]
					break
				}
			}
		}

		if ghosttyWin != nil {
			// Full pipeline: scroll-stitch capture with window metadata
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found: %s\n", ghosttyWin.Label())
			var paneIndices []int
			if paneStr := getFlag("--pane"); paneStr != "" {
				paneN, err := strconv.Atoi(paneStr)
				if err != nil || paneN < 1 {
					fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m --pane must be a positive integer (got %q)\n", paneStr)
					os.Exit(1)
				}
				paneIndices = []int{paneN - 1}
			}
			paths, err := captureAndRender(*ghosttyWin, format, outputPath, paneIndices)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
				os.Exit(1)
			}
			for _, path := range paths {
				fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", path)
				openFile(path)
			}
			return
		}

		// Direct screenshot fallback: no listWindows() needed
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Using direct screenshot (window discovery unavailable)\n")
		data, err := screencaptureWindow(wid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
			fmt.Fprintf(os.Stderr, "  Hint: check Screen Recording permission in System Settings.\n")
			os.Exit(1)
		}

		result := &CaptureResult{
			Window:      WindowInfo{ID: wid, Owner: "ghostty"},
			ContentType: ContentScreenshot,
			Data:        data,
		}
		outPath := outputPath
		if outPath == "" {
			ts := time.Now().Format("2006-01-02_15-04-05")
			outPath = defaultOutputPath(fmt.Sprintf("recap-ghostty-%s.%s", ts, format))
		}
		path, err := renderSingle(result, format, outPath, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", path)
		openFile(path)
		return
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Detecting Ghostty windows...\n")

	windows, err := listWindows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Check that Screen Recording permission is granted:\n")
		fmt.Fprintf(os.Stderr, "        System Settings → Privacy & Security → Screen Recording\n")
		os.Exit(1)
	}

	// Collect all Ghostty windows.
	var ghosttyWindows []WindowInfo
	for _, w := range windows {
		if isGhostty(w.Owner) {
			ghosttyWindows = append(ghosttyWindows, w)
		}
	}

	if len(ghosttyWindows) == 0 {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no Ghostty window found\n")
		var hasTerminals, hasAnyWindows bool
		for _, w := range windows {
			hasAnyWindows = true
			if w.Type == AppTerminal {
				hasTerminals = true
				break
			}
		}
		if hasTerminals {
			fmt.Fprintf(os.Stderr, "  Other terminal windows were detected — use \033[1mrecap detect\033[0m to select from all windows.\n")
		} else if hasAnyWindows {
			fmt.Fprintf(os.Stderr, "  No terminal windows found. Is Ghostty on another Space or minimized?\n")
			fmt.Fprintf(os.Stderr, "  Use \033[1mrecap detect --list\033[0m to see detected windows.\n")
		} else {
			fmt.Fprintf(os.Stderr, "  No windows detected at all. Check Screen Recording permission:\n")
			fmt.Fprintf(os.Stderr, "  System Settings → Privacy & Security → Screen Recording\n")
		}
		os.Exit(1)
	}

	// Target selection: --active > --title > interactive picker > first window.
	var ghosttyWin *WindowInfo

	if hasFlag("--active") {
		for i := range ghosttyWindows {
			if ghosttyWindows[i].IsActive {
				ghosttyWin = &ghosttyWindows[i]
				break
			}
		}
		if ghosttyWin == nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no active Ghostty window found\n")
			os.Exit(1)
		}
	} else if titleFilter != "" {
		// Substring match on window title.
		for i := range ghosttyWindows {
			if strings.Contains(strings.ToLower(ghosttyWindows[i].Name), strings.ToLower(titleFilter)) {
				ghosttyWin = &ghosttyWindows[i]
				break
			}
		}
		if ghosttyWin == nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no Ghostty window matching --title %q\n", titleFilter)
			fmt.Fprintf(os.Stderr, "  Available Ghostty windows:\n")
			for _, w := range ghosttyWindows {
				fmt.Fprintf(os.Stderr, "    [%d] %s\n", w.ID, w.Label())
			}
			os.Exit(1)
		}
	} else if len(ghosttyWindows) > 1 {
		// Multiple Ghostty windows, no filter — interactive picker.
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found %d Ghostty windows:\n", len(ghosttyWindows))
		for i, w := range ghosttyWindows {
			onscreen := ""
			if !w.OnScreen {
				onscreen = " \033[90m(other space)\033[0m"
			}
			fmt.Fprintf(os.Stderr, "  \033[1m%d)\033[0m %s%s\n", i+1, w.Label(), onscreen)
		}
		fmt.Fprintf(os.Stderr, "\n  Select [1-%d]: ", len(ghosttyWindows))
		var choice string
		fmt.Scanln(&choice)
		idx, err := strconv.Atoi(strings.TrimSpace(choice))
		if err != nil || idx < 1 || idx > len(ghosttyWindows) {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m invalid selection\n")
			os.Exit(1)
		}
		ghosttyWin = &ghosttyWindows[idx-1]
	} else {
		// Single Ghostty window — use it directly.
		ghosttyWin = &ghosttyWindows[0]
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found: %s\n", ghosttyWin.Label())

	// --pane N: capture only pane N (1-indexed) instead of all.
	var paneIndices []int
	if paneStr := getFlag("--pane"); paneStr != "" {
		paneN, err := strconv.Atoi(paneStr)
		if err != nil || paneN < 1 {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m --pane must be a positive integer (got %q)\n", paneStr)
			os.Exit(1)
		}
		paneIndices = []int{paneN - 1}
	} else if hasFlag("--active-pane") {
		paneIdx, err := detectActiveGhosttyPane(*ghosttyWin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m active pane detection failed: %v\n", err)
		} else if paneIdx >= 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Focused pane: %d\n", paneIdx+1)
			paneIndices = []int{paneIdx}
		}
	}

	paths, err := captureAndRender(*ghosttyWin, format, outputPath, paneIndices)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		errMsg := err.Error()
		if strings.Contains(errMsg, "timed out") {
			fmt.Fprintf(os.Stderr, "  Hint: is the Ghostty window on a different Space or obscured?\n")
		} else if strings.Contains(errMsg, "screencapture") {
			fmt.Fprintf(os.Stderr, "  Hint: check Screen Recording permission in System Settings.\n")
		} else if strings.Contains(errMsg, "render") || strings.Contains(errMsg, "chromedp") {
			fmt.Fprintf(os.Stderr, "  Hint: rendering requires Google Chrome or Chromium installed.\n")
		}
		os.Exit(1)
	}

	for _, path := range paths {
		fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", path)
		openFile(path)
	}
}
