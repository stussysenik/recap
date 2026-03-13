package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// TmuxPane describes a single tmux pane discovered across all sessions.
type TmuxPane struct {
	SessionName string // tmux session name (e.g. "main")
	WindowIndex int    // window index within session
	WindowName  string // window name (e.g. "vim")
	PaneIndex   int    // pane index within window
	PaneID      string // unique pane ID (e.g. "%0")
	Width       int    // pane width in columns
	Height      int    // pane height in rows
	Active      bool   // whether this pane is the active pane in its window
	Title       string // pane title (if set)
}

// Label returns a display string for the selector TUI.
func (p TmuxPane) Label() string {
	name := p.WindowName
	if name == "" {
		name = "shell"
	}
	active := ""
	if p.Active {
		active = " *"
	}
	return fmt.Sprintf("tmux: %s:%d.%d — %s%s", p.SessionName, p.WindowIndex, p.PaneIndex, name, active)
}

// isTmuxAvailable checks if a tmux server is running and accessible.
func isTmuxAvailable() bool {
	cmd := exec.Command("tmux", "list-sessions")
	err := cmd.Run()
	return err == nil
}

// listTmuxPanes discovers ALL panes across all tmux sessions.
// Returns nil if tmux is not running or not installed.
func listTmuxPanes() []TmuxPane {
	if !isTmuxAvailable() {
		return nil
	}

	// Format string for tmux list-panes -a:
	// session_name, window_index, window_name, pane_index, pane_id,
	// pane_width, pane_height, pane_active, pane_title
	format := "#{session_name}\t#{window_index}\t#{window_name}\t#{pane_index}\t#{pane_id}\t#{pane_width}\t#{pane_height}\t#{pane_active}\t#{pane_title}"
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", format)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var panes []TmuxPane
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 9)
		if len(fields) < 9 {
			continue
		}

		pane := TmuxPane{
			SessionName: fields[0],
			WindowIndex: atoi(fields[1]),
			WindowName:  fields[2],
			PaneIndex:   atoi(fields[3]),
			PaneID:      fields[4],
			Width:       atoi(fields[5]),
			Height:      atoi(fields[6]),
			Active:      fields[7] == "1",
			Title:       fields[8],
		}
		panes = append(panes, pane)
	}

	return panes
}

// captureTmuxPane runs `tmux capture-pane` to get the full scrollback
// content of a specific pane, including ANSI escape sequences for colors.
// Returns the raw ANSI text content.
func captureTmuxPane(paneID string) ([]byte, error) {
	// -p: output to stdout
	// -S -: start from beginning of scrollback history
	// -e: include escape sequences (ANSI colors)
	// -t: target pane by ID
	cmd := exec.Command("tmux", "capture-pane", "-p", "-S", "-", "-e", "-t", paneID)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane failed for %s: %w", paneID, err)
	}
	return out, nil
}

// atoi converts a string to int, returning 0 on failure.
func atoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}
