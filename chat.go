package main

import (
	"fmt"
	"os"
	"strings"
)

// cmdChat is a non-interactive shortcut that auto-finds the first Ghostty window
// and captures all panes without the TUI selector.
func cmdChat() {
	format := "pdf"
	if hasFlag("--png") {
		format = "png"
	}
	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Detecting Ghostty windows...\n")

	windows, err := listWindows()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Check that Screen Recording permission is granted:\n")
		fmt.Fprintf(os.Stderr, "        System Settings → Privacy & Security → Screen Recording\n")
		os.Exit(1)
	}

	// Find the first Ghostty window
	var ghosttyWin *WindowInfo
	for i := range windows {
		if isGhostty(windows[i].Owner) {
			ghosttyWin = &windows[i]
			break
		}
	}

	if ghosttyWin == nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no Ghostty window found\n")
		// Provide context-specific diagnostics
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
			fmt.Fprintf(os.Stderr, "  System Settings \u2192 Privacy & Security \u2192 Screen Recording\n")
		}
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found: %s\n", ghosttyWin.Label())

	// Capture all panes (paneIndices=nil means all)
	paths, err := captureAndRender(*ghosttyWin, format, outputPath, nil)
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
