package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// runDetectSelector displays an interactive TUI for selecting from a mixed list
// of windows and tmux panes. Returns the selected items, or nil if cancelled.
// preSelectedIdx is the index of the item to pre-select, or -1 for no pre-selection.
func runDetectSelector(items []DetectItem, preSelectedIdx int) []DetectItem {
	if len(items) == 0 {
		return nil
	}

	// Pre-scan Ghostty windows for split pane counts
	paneCounts := make(map[int]int) // itemIdx -> pane count
	for i, d := range items {
		if d.Window != nil && isGhostty(d.Window.Owner) {
			if n := countGhosttyPanes(*d.Window); n > 1 {
				paneCounts[i] = n
			}
		}
	}

	// Build flat list of selectable items with group headers
	type entry struct {
		itemIdx  int  // -1 for group headers
		label    string
		isHeader bool
	}

	var entries []entry

	// Group 0: cmux Workspaces
	var cmuxEntries []int
	for i, d := range items {
		if d.Cmux != nil {
			cmuxEntries = append(cmuxEntries, i)
		}
	}
	if len(cmuxEntries) > 0 {
		entries = append(entries, entry{itemIdx: -1, label: "cmux Workspaces:", isHeader: true})
		for _, idx := range cmuxEntries {
			s := items[idx].Cmux
			label := s.Label() + " \033[36m[text]\033[0m"
			if s.Here {
				label += " \033[35m(this shell)\033[0m"
			}
			entries = append(entries, entry{itemIdx: idx, label: label})
		}
		entries = append(entries, entry{itemIdx: -1, label: "", isHeader: true}) // spacer
	}

	// Group 1: tmux Panes
	var tmuxEntries []int
	for i, d := range items {
		if d.Tmux != nil {
			tmuxEntries = append(tmuxEntries, i)
		}
	}
	if len(tmuxEntries) > 0 {
		entries = append(entries, entry{itemIdx: -1, label: "tmux Panes:", isHeader: true})
		for _, idx := range tmuxEntries {
			pane := items[idx].Tmux
			label := pane.Label() + " \033[36m[text]\033[0m"
			entries = append(entries, entry{itemIdx: idx, label: label})
		}
		entries = append(entries, entry{itemIdx: -1, label: "", isHeader: true}) // spacer
	}

	// Group 2-4: Windows by type (Terminals, Browsers, Desktop)
	typeOrder := []AppType{AppTerminal, AppBrowser, AppGeneric}
	for _, t := range typeOrder {
		var windowsInGroup []int
		for i, d := range items {
			if d.Window != nil && d.Window.Type == t {
				windowsInGroup = append(windowsInGroup, i)
			}
		}
		if len(windowsInGroup) == 0 {
			continue
		}

		entries = append(entries, entry{itemIdx: -1, label: t.String() + "s:", isHeader: true})
		for _, idx := range windowsInGroup {
			w := items[idx].Window
			label := w.Label() + " \033[33m[screenshot]\033[0m"
			if !w.OnScreen {
				label += " \033[90m(other space)\033[0m"
			}
			if n, ok := paneCounts[idx]; ok {
				label = fmt.Sprintf("%s (%d panes)", label, n)
			}
			entries = append(entries, entry{itemIdx: idx, label: label})
		}
		entries = append(entries, entry{itemIdx: -1, label: "", isHeader: true}) // spacer
	}

	// Remove trailing spacer
	if len(entries) > 0 && entries[len(entries)-1].isHeader && entries[len(entries)-1].label == "" {
		entries = entries[:len(entries)-1]
	}

	selected := make(map[int]bool) // itemIdx -> selected

	// Pre-select the active item if specified
	if preSelectedIdx >= 0 && preSelectedIdx < len(items) {
		selected[preSelectedIdx] = true
	}

	cursor := 0

	// Position cursor on pre-selected item, or first selectable item
	if preSelectedIdx >= 0 {
		// Find entry index for this item
		for i, e := range entries {
			if !e.isHeader && e.itemIdx == preSelectedIdx {
				cursor = i
				break
			}
		}
	} else {
		// Original behavior: first selectable item
		for cursor < len(entries) && entries[cursor].isHeader {
			cursor++
		}
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
		fmt.Print("  \033[1;38;5;105m\u2502\033[0m  \033[1;97m\u25c9 recap detect\033[0m \033[90m\u2014 select targets\033[0m              \033[1;38;5;105m\u2502\033[0m\r\n")
		fmt.Print("  \033[1;38;5;105m\u2514\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2518\033[0m\r\n")
		fmt.Print("\r\n")

		for i, e := range entries {
			if e.isHeader {
				if e.label == "" {
					fmt.Print("\r\n")
				} else {
					fmt.Printf("  \033[1;93m%s\033[0m\r\n", e.label)
				}
				continue
			}

			prefix := "    "
			if i == cursor {
				prefix = "  \033[1;38;5;105m>\033[0m "
			}

			check := "[ ]"
			if selected[e.itemIdx] {
				check = "\033[32m[x]\033[0m"
			}

			label := e.label
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
				cursor = len(entries) - 1
			}
			if cursor >= len(entries) {
				cursor = 0
			}
			if !entries[cursor].isHeader {
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
				if cursor < len(entries) && !entries[cursor].isHeader {
					idx := entries[cursor].itemIdx
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
					for _, e := range entries {
						if !e.isHeader {
							selected[e.itemIdx] = true
						}
					}
				}
			case 13: // Enter — confirm (only if something is selected)
				if len(selected) == 0 {
					break
				}
				var result []DetectItem
				for idx := range selected {
					result = append(result, items[idx])
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
// preSelectedIdx is the index of the pane to pre-select, or -1 for no pre-selection.
func runPaneSelector(w WindowInfo, panes []PaneInfo, preSelectedIdx int) []PaneInfo {
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
		label := fmt.Sprintf("Pane %d \u2014 %s (%dx%d)", p.Index+1, hint, p.Width, p.Height)
		items = append(items, item{paneIdx: p.Index, label: label})
	}

	selected := make(map[int]bool) // paneIdx -> selected

	// Pre-select the specified pane if valid
	if preSelectedIdx >= 0 && preSelectedIdx < len(panes) {
		selected[panes[preSelectedIdx].Index] = true
	}

	cursor := 0

	// Position cursor on pre-selected pane if specified
	if preSelectedIdx >= 0 && preSelectedIdx < len(items) {
		cursor = preSelectedIdx
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

// renderDetectList prints a non-interactive list of windows, tmux panes, and cmux surfaces (for --list flag).
func renderDetectList(windows []WindowInfo, tmuxPanes []TmuxPane, cmuxSurfaces []CmuxSurface) {
	fmt.Println()

	// cmux surfaces
	if len(cmuxSurfaces) > 0 {
		fmt.Printf("  \033[1;93mcmux Workspaces:\033[0m\n")
		for _, s := range cmuxSurfaces {
			badge := ""
			if s.Active {
				badge = " \033[32m*active\033[0m"
			}
			if s.Here {
				badge += " \033[35m(this shell)\033[0m"
			}
			fmt.Printf("    \033[36m[text]\033[0m %s%s\n", s.Label(), badge)
		}
		fmt.Println()
	}

	// tmux panes
	if len(tmuxPanes) > 0 {
		fmt.Printf("  \033[1;93mtmux Panes:\033[0m\n")
		for _, p := range tmuxPanes {
			active := ""
			if p.Active {
				active = " \033[32m*active\033[0m"
			}
			fmt.Printf("    \033[36m[text]\033[0m %s \033[90m(%dx%d)\033[0m%s\n",
				p.Label(), p.Width, p.Height, active)
		}
		fmt.Println()
	}

	// Windows by type
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
			onscreen := ""
			if !w.OnScreen {
				onscreen = " \033[90m(other space)\033[0m"
			}
			active := ""
			if w.IsActive {
				active = " \033[35m(this window)\033[0m"
			}
			fmt.Printf("    \033[33m[screenshot]\033[0m \033[90m[%d]\033[0m %s \033[90m\u2014 %s\033[0m (%dx%d @%d,%d)%s%s%s\n",
				w.ID, strings.ToLower(w.Owner), name, w.Width, w.Height, w.X, w.Y, extra, onscreen, active)
		}
		fmt.Println()
	}
}
