package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// runSelector displays an interactive TUI for selecting windows.
// Returns the selected windows, or nil if cancelled.
func runSelector(windows []WindowInfo) []WindowInfo {
	if len(windows) == 0 {
		return nil
	}

	// Pre-scan Ghostty windows for split pane counts
	paneCounts := make(map[int]int) // windowIdx -> pane count
	for i, w := range windows {
		if isGhostty(w.Owner) {
			if n := countGhosttyPanes(w); n > 1 {
				paneCounts[i] = n
			}
		}
	}

	order := []AppType{AppTerminal, AppBrowser, AppGeneric}

	// Build flat list of selectable items with group headers
	type item struct {
		windowIdx int  // -1 for group headers
		label     string
		isHeader  bool
	}

	var items []item
	for _, t := range order {
		var windowsInGroup []int
		for i, w := range windows {
			if w.Type == t {
				windowsInGroup = append(windowsInGroup, i)
			}
		}
		if len(windowsInGroup) == 0 {
			continue
		}

		items = append(items, item{windowIdx: -1, label: t.String() + "s:", isHeader: true})
		for _, idx := range windowsInGroup {
			label := windows[idx].Label()
			if n, ok := paneCounts[idx]; ok {
				label = fmt.Sprintf("%s (%d panes)", label, n)
			}
			items = append(items, item{windowIdx: idx, label: label})
		}
		items = append(items, item{windowIdx: -1, label: "", isHeader: true}) // spacer
	}

	// Remove trailing spacer
	if len(items) > 0 && items[len(items)-1].isHeader && items[len(items)-1].label == "" {
		items = items[:len(items)-1]
	}

	selected := make(map[int]bool) // windowIdx -> selected
	cursor := 0

	// Move cursor to first selectable item
	for cursor < len(items) && items[cursor].isHeader {
		cursor++
	}

	// Enter raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error entering raw mode: %v\n", err)
		return nil
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Hide cursor
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	render := func() {
		// Clear screen and move to top
		fmt.Print("\033[2J\033[H")

		// Header
		fmt.Print("  \033[1;38;5;105m\u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510\033[0m\r\n")
		fmt.Print("  \033[1;38;5;105m\u2502\033[0m  \033[1;97m\u25c9 recap detect\033[0m \033[90m\u2014 select windows\033[0m               \033[1;38;5;105m\u2502\033[0m\r\n")
		fmt.Print("  \033[1;38;5;105m\u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\033[0m\r\n")
		fmt.Print("\r\n")

		for i, it := range items {
			if it.isHeader {
				if it.label == "" {
					fmt.Print("\r\n")
				} else {
					fmt.Printf("  \033[1;93m%s\033[0m\r\n", it.label)
				}
				continue
			}

			prefix := "    "
			if i == cursor {
				prefix = "  \033[1;38;5;105m>\033[0m "
			}

			check := "[ ]"
			if selected[it.windowIdx] {
				check = "\033[32m[x]\033[0m"
			}

			label := it.label
			if i == cursor {
				label = "\033[1;97m" + label + "\033[0m"
			} else {
				label = "\033[37m" + label + "\033[0m"
			}

			fmt.Printf("%s%s %s\r\n", prefix, check, label)
		}

		// Count selected
		count := len(selected)

		fmt.Print("\r\n")
		fmt.Printf("  \033[90m\u2191\u2193 move \u00b7 space select \u00b7 a all \u00b7 enter confirm \u00b7 q quit\033[0m\r\n")
		if count > 0 {
			fmt.Printf("  \033[32m%d selected\033[0m\r\n", count)
		}
	}

	moveCursor := func(delta int) {
		for {
			cursor += delta
			if cursor < 0 {
				cursor = len(items) - 1
			}
			if cursor >= len(items) {
				cursor = 0
			}
			if !items[cursor].isHeader {
				break
			}
		}
	}

	render()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return nil
		}

		switch {
		// Escape sequences (arrows)
		case n == 3 && buf[0] == 27 && buf[1] == '[':
			switch buf[2] {
			case 'A': // Up
				moveCursor(-1)
			case 'B': // Down
				moveCursor(1)
			}

		// Single byte keys
		case n == 1:
			switch buf[0] {
			case 'k': // vim up
				moveCursor(-1)
			case 'j': // vim down
				moveCursor(1)
			case ' ': // toggle selection
				if cursor < len(items) && !items[cursor].isHeader {
					idx := items[cursor].windowIdx
					if selected[idx] {
						delete(selected, idx)
					} else {
						selected[idx] = true
					}
				}
			case 'a': // select all / deselect all
				if len(selected) > 0 {
					selected = make(map[int]bool)
				} else {
					for _, it := range items {
						if !it.isHeader {
							selected[it.windowIdx] = true
						}
					}
				}
			case 13: // Enter — confirm (only if something is selected)
				if len(selected) == 0 {
					break
				}
				var result []WindowInfo
				for idx := range selected {
					result = append(result, windows[idx])
				}
				// Clear screen before returning
				fmt.Print("\033[2J\033[H")
				return result
			case 'q', 27: // q or Escape — cancel
				fmt.Print("\033[2J\033[H")
				return nil
			case 3: // Ctrl+C
				fmt.Print("\033[2J\033[H")
				return nil
			}
		}

		render()
	}
}

// runPaneSelector displays an interactive TUI for selecting which panes to capture.
// Returns the selected panes, or nil if cancelled.
func runPaneSelector(w WindowInfo, panes []PaneInfo) []PaneInfo {
	if len(panes) == 0 {
		return nil
	}

	type item struct {
		paneIdx int
		label   string
	}

	var items []item
	for _, p := range panes {
		hint := panePositionHint(p, panes)
		label := fmt.Sprintf("Pane %d — %s (%dx%d)", p.Index+1, hint, p.Width, p.Height)
		items = append(items, item{paneIdx: p.Index, label: label})
	}

	selected := make(map[int]bool) // paneIdx -> selected
	cursor := 0

	// Enter raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error entering raw mode: %v\n", err)
		return nil
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Hide cursor
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")

	render := func() {
		fmt.Print("\033[2J\033[H")

		// Header
		fmt.Print("  \033[1;38;5;105m\u250c\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2510\033[0m\r\n")
		fmt.Printf("  \033[1;38;5;105m\u2502\033[0m  \033[1;97m\u25c9 recap detect\033[0m \033[90m\u2014 select panes\033[0m                 \033[1;38;5;105m\u2502\033[0m\r\n")
		fmt.Print("  \033[1;38;5;105m\u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\033[0m\r\n")
		fmt.Printf("  \033[90m%s\033[0m\r\n", w.Label())
		fmt.Print("\r\n")

		for i, it := range items {
			prefix := "    "
			if i == cursor {
				prefix = "  \033[1;38;5;105m>\033[0m "
			}

			check := "[ ]"
			if selected[it.paneIdx] {
				check = "\033[32m[x]\033[0m"
			}

			label := it.label
			if i == cursor {
				label = "\033[1;97m" + label + "\033[0m"
			} else {
				label = "\033[37m" + label + "\033[0m"
			}

			fmt.Printf("%s%s %s\r\n", prefix, check, label)
		}

		count := len(selected)
		fmt.Print("\r\n")
		fmt.Printf("  \033[90m\u2191\u2193 move \u00b7 space select \u00b7 a all \u00b7 enter confirm \u00b7 q quit\033[0m\r\n")
		if count > 0 {
			fmt.Printf("  \033[32m%d selected\033[0m\r\n", count)
		}
	}

	render()

	buf := make([]byte, 3)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return nil
		}

		switch {
		case n == 3 && buf[0] == 27 && buf[1] == '[':
			switch buf[2] {
			case 'A': // Up
				cursor--
				if cursor < 0 {
					cursor = len(items) - 1
				}
			case 'B': // Down
				cursor++
				if cursor >= len(items) {
					cursor = 0
				}
			}

		case n == 1:
			switch buf[0] {
			case 'k':
				cursor--
				if cursor < 0 {
					cursor = len(items) - 1
				}
			case 'j':
				cursor++
				if cursor >= len(items) {
					cursor = 0
				}
			case ' ':
				idx := items[cursor].paneIdx
				if selected[idx] {
					delete(selected, idx)
				} else {
					selected[idx] = true
				}
			case 'a':
				if len(selected) > 0 {
					selected = make(map[int]bool)
				} else {
					for _, it := range items {
						selected[it.paneIdx] = true
					}
				}
			case 13: // Enter
				if len(selected) == 0 {
					break
				}
				var result []PaneInfo
				for _, p := range panes {
					if selected[p.Index] {
						result = append(result, p)
					}
				}
				fmt.Print("\033[2J\033[H")
				return result
			case 'q', 27: // q or Escape
				fmt.Print("\033[2J\033[H")
				return nil
			case 3: // Ctrl+C
				fmt.Print("\033[2J\033[H")
				return nil
			}
		}

		render()
	}
}

// panePositionHint derives a position label (e.g. "left", "top-right")
// by comparing a pane's coordinates to the others.
func panePositionHint(pane PaneInfo, allPanes []PaneInfo) string {
	if len(allPanes) <= 1 {
		return "main"
	}

	// Calculate center of all panes to determine relative position
	var totalCX, totalCY float64
	for _, p := range allPanes {
		totalCX += float64(p.X) + float64(p.Width)/2
		totalCY += float64(p.Y) + float64(p.Height)/2
	}
	avgCX := totalCX / float64(len(allPanes))
	avgCY := totalCY / float64(len(allPanes))

	cx := float64(pane.X) + float64(pane.Width)/2
	cy := float64(pane.Y) + float64(pane.Height)/2

	// Determine horizontal and vertical position relative to center
	isLeft := cx < avgCX-20
	isRight := cx > avgCX+20
	isTop := cy < avgCY-20
	isBottom := cy > avgCY+20

	switch {
	case isTop && isLeft:
		return "top-left"
	case isTop && isRight:
		return "top-right"
	case isBottom && isLeft:
		return "bottom-left"
	case isBottom && isRight:
		return "bottom-right"
	case isLeft:
		return "left"
	case isRight:
		return "right"
	case isTop:
		return "top"
	case isBottom:
		return "bottom"
	default:
		return "center"
	}
}

// groupLabel returns a section header string for an AppType.
func groupLabel(t AppType) string {
	switch t {
	case AppTerminal:
		return "Terminals"
	case AppBrowser:
		return "Browsers"
	default:
		return "Desktop Apps"
	}
}

// renderWindowList prints a non-interactive window list (for --list flag).
func renderWindowList(windows []WindowInfo) {
	fmt.Println()

	typeOrder := []AppType{AppTerminal, AppBrowser, AppGeneric}
	for _, t := range typeOrder {
		var group []WindowInfo
		for _, w := range windows {
			if w.Type == t {
				group = append(group, w)
			}
		}
		if len(group) == 0 {
			continue
		}

		fmt.Printf("  \033[1;93m%s:\033[0m\n", groupLabel(t))
		for _, w := range group {
			name := w.Name
			if name == "" {
				name = "(untitled)"
			}
			if len(name) > 50 {
				name = name[:47] + "..."
			}
			extra := ""
			if isGhostty(w.Owner) {
				if n := countGhosttyPanes(w); n > 1 {
					extra = fmt.Sprintf(" \033[36m(%d panes)\033[0m", n)
				}
			}
			fmt.Printf("    \033[90m[%d]\033[0m %s \033[90m— %s\033[0m (%dx%d @%d,%d)%s\n",
				w.ID, strings.ToLower(w.Owner), name, w.Width, w.Height, w.X, w.Y, extra)
		}
		fmt.Println()
	}
}
