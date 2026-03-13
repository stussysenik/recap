package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CmuxSurface describes a single terminal surface discovered via cmux CLI.
// Each surface lives inside a workspace → pane hierarchy.
type CmuxSurface struct {
	WorkspaceRef   string // e.g. "workspace:3"
	WorkspaceTitle string // e.g. "✳ GitHub Actions Centralization"
	WorkspaceIndex int
	PaneRef        string // e.g. "pane:5"
	PaneIndex      int
	SurfaceRef     string // e.g. "surface:5"
	SurfaceTitle   string // e.g. "✳ GitHub Actions Centralization"
	SurfaceType    string // "terminal" or "browser"
	SurfaceIndex   int
	Active         bool // whether this is the active surface
	Here           bool // whether recap is running in this surface
}

// Label returns a display string for the selector TUI.
func (s CmuxSurface) Label() string {
	title := s.SurfaceTitle
	if title == "" {
		title = s.WorkspaceTitle
	}
	if title == "" {
		title = "(untitled)"
	}
	return fmt.Sprintf("cmux: %s > %s", s.WorkspaceTitle, title)
}

// --- JSON types matching `cmux tree --all --json` output ---

type cmuxTreeResponse struct {
	Active  cmuxActiveRef  `json:"active"`
	Caller  cmuxActiveRef  `json:"caller"`
	Windows []cmuxWindow   `json:"windows"`
}

type cmuxActiveRef struct {
	WindowRef    string `json:"window_ref"`
	WorkspaceRef string `json:"workspace_ref"`
	PaneRef      string `json:"pane_ref"`
	SurfaceRef   string `json:"surface_ref"`
	SurfaceType  string `json:"surface_type"`
}

type cmuxWindow struct {
	Ref            string          `json:"ref"`
	Index          int             `json:"index"`
	Active         bool            `json:"active"`
	Current        bool            `json:"current"`
	WorkspaceCount int             `json:"workspace_count"`
	Workspaces     []cmuxWorkspace `json:"workspaces"`
}

type cmuxWorkspace struct {
	Ref      string     `json:"ref"`
	Title    string     `json:"title"`
	Index    int        `json:"index"`
	Active   bool       `json:"active"`
	Selected bool       `json:"selected"`
	Pinned   bool       `json:"pinned"`
	Panes    []cmuxPane `json:"panes"`
}

type cmuxPane struct {
	Ref          string            `json:"ref"`
	Index        int               `json:"index"`
	Active       bool              `json:"active"`
	Focused      bool              `json:"focused"`
	SurfaceCount int               `json:"surface_count"`
	Surfaces     []cmuxSurfaceJSON `json:"surfaces"`
}

type cmuxSurfaceJSON struct {
	Ref            string  `json:"ref"`
	Title          string  `json:"title"`
	Type           string  `json:"type"`
	Index          int     `json:"index"`
	IndexInPane    int     `json:"index_in_pane"`
	PaneRef        string  `json:"pane_ref"`
	Active         bool    `json:"active"`
	Focused        bool    `json:"focused"`
	Selected       bool    `json:"selected"`
	SelectedInPane bool    `json:"selected_in_pane"`
	Here           bool    `json:"here"`
	URL            *string `json:"url"`
}

// cmuxSocketPath returns the path to the cmux Unix domain socket.
func cmuxSocketPath() string {
	if p := os.Getenv("CMUX_SOCKET_PATH"); p != "" {
		return p
	}
	return "/tmp/cmux.sock"
}

// isCmuxAvailable checks if cmux is running by verifying the socket
// exists and the server responds to ping.
func isCmuxAvailable() bool {
	// Check socket exists
	if _, err := os.Stat(cmuxSocketPath()); err != nil {
		return false
	}

	// Verify server responds
	cmd := exec.Command("cmux", "ping")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "PONG"
}

// listCmuxSurfaces discovers all terminal surfaces via `cmux tree --all --json`.
// Returns nil if cmux is unavailable or on error.
func listCmuxSurfaces() []CmuxSurface {
	if !isCmuxAvailable() {
		return nil
	}

	cmd := exec.Command("cmux", "tree", "--all", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var tree cmuxTreeResponse
	if err := json.Unmarshal(out, &tree); err != nil {
		return nil
	}

	var surfaces []CmuxSurface
	for _, win := range tree.Windows {
		for _, ws := range win.Workspaces {
			for _, pane := range ws.Panes {
				for _, surf := range pane.Surfaces {
					// Skip browser surfaces — read-screen doesn't work on them
					if surf.Type != "terminal" {
						continue
					}

					surfaces = append(surfaces, CmuxSurface{
						WorkspaceRef:   ws.Ref,
						WorkspaceTitle: ws.Title,
						WorkspaceIndex: ws.Index,
						PaneRef:        pane.Ref,
						PaneIndex:      pane.Index,
						SurfaceRef:     surf.Ref,
						SurfaceTitle:   surf.Title,
						SurfaceType:    surf.Type,
						SurfaceIndex:   surf.Index,
						Active:         surf.Active,
						Here:           surf.Here,
					})
				}
			}
		}
	}

	return surfaces
}

// captureCmuxSurface captures the full scrollback text of a cmux surface.
// Uses `cmux read-screen --workspace <ref> --scrollback --lines 10000`.
// The --workspace flag is required; --surface alone fails with "not a terminal".
func captureCmuxSurface(workspaceRef, surfaceRef string) ([]byte, error) {
	args := []string{"read-screen",
		"--workspace", workspaceRef,
		"--surface", surfaceRef,
		"--scrollback",
		"--lines", "10000",
	}
	cmd := exec.Command("cmux", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cmux read-screen failed for %s/%s: %w", workspaceRef, surfaceRef, err)
	}
	return out, nil
}

// countCmuxWorkspaces returns the number of unique workspaces across all surfaces.
func countCmuxWorkspaces(surfaces []CmuxSurface) int {
	seen := make(map[string]bool)
	for _, s := range surfaces {
		seen[s.WorkspaceRef] = true
	}
	return len(seen)
}
