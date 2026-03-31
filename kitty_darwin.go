//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// KittyOSWindow represents a top-level Kitty OS window from the `ls` command.
type KittyOSWindow struct {
	ID         int        `json:"id"`
	IsActive   bool       `json:"is_active"`
	IsFocused  bool       `json:"is_focused"`
	Tabs       []KittyTab `json:"tabs"`
	PlatformID *int       `json:"platform_window_id"`
}

// KittyTab represents a tab within a Kitty OS window.
type KittyTab struct {
	ID       int         `json:"id"`
	IsActive bool        `json:"is_active"`
	Title    string      `json:"title"`
	Windows  []KittyPane `json:"windows"`
}

// KittyPane represents a single pane (Kitty calls them "windows") within a tab.
// This is the richest data source — it includes PID, CWD, foreground process,
// title, and dimensions per pane.
type KittyPane struct {
	ID           int      `json:"id"`
	IsActive     bool     `json:"is_active"`
	IsFocused    bool     `json:"is_focused"`
	Title        string   `json:"title"`
	PID          int      `json:"pid"`
	CWD          string   `json:"cwd"`
	Cmdline      []string `json:"cmdline"`
	FgProcesses  []kittyFgProc `json:"foreground_processes"`
	Columns      int      `json:"columns"`
	Lines        int      `json:"lines"`
}

// kittyFgProc describes a foreground process in a Kitty pane.
type kittyFgProc struct {
	PID     int      `json:"pid"`
	CWD     string   `json:"cwd"`
	Cmdline []string `json:"cmdline"`
}

// ForegroundProcess returns a display string for what's running in this pane.
func (p KittyPane) ForegroundProcess() string {
	if len(p.FgProcesses) > 0 && len(p.FgProcesses[0].Cmdline) > 0 {
		cmd := p.FgProcesses[0].Cmdline[0]
		// Strip path prefix for display
		if idx := strings.LastIndex(cmd, "/"); idx >= 0 {
			cmd = cmd[idx+1:]
		}
		return cmd
	}
	if len(p.Cmdline) > 0 {
		cmd := p.Cmdline[0]
		if idx := strings.LastIndex(cmd, "/"); idx >= 0 {
			cmd = cmd[idx+1:]
		}
		return cmd
	}
	return ""
}

// kittyResponse wraps the JSON-RPC response from the Kitty socket protocol.
type kittyResponse struct {
	Ok   bool            `json:"ok"`
	Data json.RawMessage `json:"data"`
	// Error info when ok=false
	Error   string `json:"error,omitempty"`
	NoData  bool   `json:"no_data,omitempty"`
}

// findKittySockets discovers Kitty remote control Unix sockets.
// Kitty creates sockets at /tmp/kitty-<uid>-<pid>/control when started
// with --listen-on or when allow_remote_control is enabled.
// Also checks the KITTY_LISTEN_ON environment variable.
func findKittySockets() []string {
	var sockets []string

	// Check KITTY_LISTEN_ON first
	if listenOn := os.Getenv("KITTY_LISTEN_ON"); listenOn != "" {
		// Strip unix: prefix if present
		path := strings.TrimPrefix(listenOn, "unix:")
		if _, err := os.Stat(path); err == nil {
			sockets = append(sockets, path)
		}
	}

	// Glob for /tmp/kitty-*/control sockets
	matches, err := filepath.Glob("/tmp/kitty-*/control")
	if err == nil {
		for _, m := range matches {
			// Avoid duplicates with KITTY_LISTEN_ON
			dup := false
			for _, s := range sockets {
				if s == m {
					dup = true
					break
				}
			}
			if !dup {
				sockets = append(sockets, m)
			}
		}
	}

	return sockets
}

// kittyLS sends the `ls` command over the Kitty remote control protocol
// and returns the full window > tab > pane hierarchy.
//
// The protocol is: ESC P @kitty-cmd <JSON> ESC \
// Response is: ESC P @kitty-cmd <JSON> ESC \
func kittyLS(socketPath string) ([]KittyOSWindow, error) {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to kitty socket: %w", err)
	}
	defer conn.Close()

	// Set read/write deadlines
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Build the ls command payload
	payload := map[string]interface{}{
		"cmd":     "ls",
		"version": []int{0, 30, 0},
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Send: ESC P @kitty-cmd <JSON> ESC \
	msg := fmt.Sprintf("\x1bP@kitty-cmd%s\x1b\\", payloadJSON)
	if _, err := conn.Write([]byte(msg)); err != nil {
		return nil, fmt.Errorf("write to kitty socket: %w", err)
	}

	// Read response — accumulate until we see ESC \ or timeout
	var buf []byte
	tmp := make([]byte, 64*1024)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		// Check for response terminator
		if len(buf) > 2 && buf[len(buf)-2] == 0x1b && buf[len(buf)-1] == '\\' {
			break
		}
		if err != nil {
			break
		}
	}

	// Parse response: strip ESC P @kitty-cmd prefix and ESC \ suffix
	response := string(buf)
	prefix := "\x1bP@kitty-cmd"
	suffix := "\x1b\\"
	if idx := strings.Index(response, prefix); idx >= 0 {
		response = response[idx+len(prefix):]
	}
	if idx := strings.LastIndex(response, suffix); idx >= 0 {
		response = response[:idx]
	}

	// First parse the kitty response wrapper
	var resp kittyResponse
	if err := json.Unmarshal([]byte(response), &resp); err != nil {
		// Try parsing directly as window list (some kitty versions)
		var windows []KittyOSWindow
		if err2 := json.Unmarshal([]byte(response), &windows); err2 != nil {
			return nil, fmt.Errorf("parse kitty response: %w (raw: %.200s)", err, response)
		}
		return windows, nil
	}

	if !resp.Ok {
		return nil, fmt.Errorf("kitty error: %s", resp.Error)
	}

	var windows []KittyOSWindow
	if err := json.Unmarshal(resp.Data, &windows); err != nil {
		return nil, fmt.Errorf("parse kitty window data: %w", err)
	}

	return windows, nil
}

// kittyGetText retrieves the text content (scrollback) of a specific Kitty pane
// via the get-text remote control command.
func kittyGetText(socketPath string, paneID int) ([]byte, error) {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to kitty socket: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	payload := map[string]interface{}{
		"cmd":     "get-text",
		"version": []int{0, 30, 0},
		"payload": map[string]interface{}{
			"match":      fmt.Sprintf("id:%d", paneID),
			"ansi":       true,
			"extent":     "all",
		},
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	msg := fmt.Sprintf("\x1bP@kitty-cmd%s\x1b\\", payloadJSON)
	if _, err := conn.Write([]byte(msg)); err != nil {
		return nil, fmt.Errorf("write to kitty socket: %w", err)
	}

	var buf []byte
	tmp := make([]byte, 256*1024)
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if len(buf) > 2 && buf[len(buf)-2] == 0x1b && buf[len(buf)-1] == '\\' {
			break
		}
		if err != nil {
			break
		}
	}

	response := string(buf)
	prefix := "\x1bP@kitty-cmd"
	suffix := "\x1b\\"
	if idx := strings.Index(response, prefix); idx >= 0 {
		response = response[idx+len(prefix):]
	}
	if idx := strings.LastIndex(response, suffix); idx >= 0 {
		response = response[:idx]
	}

	var resp kittyResponse
	if err := json.Unmarshal([]byte(response), &resp); err != nil {
		return nil, fmt.Errorf("parse kitty get-text response: %w", err)
	}
	if !resp.Ok {
		return nil, fmt.Errorf("kitty get-text error: %s", resp.Error)
	}

	// Data is a string with the text content
	var text string
	if err := json.Unmarshal(resp.Data, &text); err != nil {
		// Try returning raw data
		return resp.Data, nil
	}

	return []byte(text), nil
}

// listKittyPanes discovers all Kitty panes across all sockets.
// Returns nil if no Kitty sockets are found or if remote control is disabled.
func listKittyPanes() ([]KittyPane, string) {
	sockets := findKittySockets()
	if len(sockets) == 0 {
		return nil, ""
	}

	// Try each socket until one works
	for _, sock := range sockets {
		windows, err := kittyLS(sock)
		if err != nil {
			continue
		}

		var panes []KittyPane
		for _, osWin := range windows {
			for _, tab := range osWin.Tabs {
				for i := range tab.Windows {
					panes = append(panes, tab.Windows[i])
				}
			}
		}

		if len(panes) > 0 {
			return panes, sock
		}
	}

	return nil, ""
}
