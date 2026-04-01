package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ContentType describes the kind of captured content.
type ContentType int

const (
	ContentTextANSI ContentType = iota
	ContentTextPlain
	ContentScreenshot
	ContentMultiImage // multiple screenshots for scroll-stitch capture
)

// PaneCapture holds the captured content for a single pane.
type PaneCapture struct {
	Index       int
	ContentType ContentType
	Data        []byte   // single screenshot
	Images      [][]byte // multi-image (scroll-stitch)
	SearchText  []byte   // copy/pasteable text extracted from the terminal when available
	Title       string
}

// CaptureResult holds the output of a window capture.
type CaptureResult struct {
	Window      WindowInfo
	ContentType ContentType
	Data        []byte
	SearchText  []byte
	Title       string
	Panes       []PaneCapture // non-nil when multi-pane
}

// CaptureAdapter captures content from a window.
type CaptureAdapter interface {
	Capture(w WindowInfo) (*CaptureResult, error)
}

// DetectItem is a union type representing a capturable target discovered
// during detection. Exactly one field is non-nil. Extended to support
// individual terminal tabs, Kitty panes, and orphan TTY sessions.
type DetectItem struct {
	Window   *WindowInfo
	Tmux     *TmuxPane
	Cmux     *CmuxSurface
	Tab      *TabItem       // AX-detected tab within a terminal window
	Kitty    *KittyPaneItem // Kitty pane discovered via socket protocol
	TTYShell *TTYShellItem  // Orphan shell on a TTY (no window correlation)
}

// TabItem represents a single tab detected via AXTabGroup.
// Links back to the parent window for capture purposes.
type TabItem struct {
	ParentWindow WindowInfo // the terminal window this tab belongs to
	AXTab        AXTab      // tab metadata from Accessibility API
}

// KittyPaneItem represents a single Kitty pane with its rich metadata
// and the socket path needed for capture.
type KittyPaneItem struct {
	Pane       KittyPane
	SocketPath string
	TabTitle   string // title of the parent tab
}

// TTYShellItem represents a shell process on a TTY that couldn't be
// correlated to any known terminal window. Typically SSH sessions,
// screen sessions, or terminals we lack window info for.
type TTYShellItem struct {
	Shell   ShellProc
	Session *ShellSession // optional enrichment from shell hook registry
}

// Label returns a display string for the selector.
func (d DetectItem) Label() string {
	if d.Cmux != nil {
		return d.Cmux.Label()
	}
	if d.Tmux != nil {
		return d.Tmux.Label()
	}
	if d.Tab != nil {
		title := d.Tab.AXTab.Title
		if title == "" {
			title = "(untitled)"
		}
		return fmt.Sprintf("%s — tab %d: %s", strings.ToLower(d.Tab.ParentWindow.Owner), d.Tab.AXTab.Tab+1, title)
	}
	if d.Kitty != nil {
		title := d.Kitty.Pane.Title
		if title == "" {
			title = d.Kitty.Pane.ForegroundProcess()
		}
		if title == "" {
			title = "(shell)"
		}
		cwd := d.Kitty.Pane.CWD
		if cwd != "" {
			if home, err := os.UserHomeDir(); err == nil {
				cwd = strings.Replace(cwd, home, "~", 1)
			}
			return fmt.Sprintf("kitty: %s — %s", title, cwd)
		}
		return fmt.Sprintf("kitty: %s", title)
	}
	if d.TTYShell != nil {
		s := d.TTYShell
		label := fmt.Sprintf("%s (PID %d, %s)", s.Shell.Comm, s.Shell.PID, s.Shell.TTY)
		if s.Session != nil && s.Session.CWD != "" {
			cwd := s.Session.CWD
			if home, err := os.UserHomeDir(); err == nil {
				cwd = strings.Replace(cwd, home, "~", 1)
			}
			label += fmt.Sprintf(" — %s", cwd)
		}
		return label
	}
	return d.Window.Label()
}

// CaptureMethod returns "text" for tmux/cmux/kitty/tty, "screenshot" for windows.
func (d DetectItem) CaptureMethod() string {
	if d.Cmux != nil {
		return "text"
	}
	if d.Tmux != nil {
		return "text"
	}
	if d.Kitty != nil {
		return "text"
	}
	if d.TTYShell != nil {
		return "text"
	}
	if d.Tab != nil {
		return "screenshot"
	}
	return "screenshot"
}

// adapterFor returns the appropriate capture adapter for a window.
func adapterFor(w WindowInfo) CaptureAdapter {
	switch w.Type {
	case AppTerminal:
		if isGhostty(w.Owner) {
			return &GhosttyAdapter{}
		}
		return &TerminalAdapter{}
	case AppBrowser:
		return &BrowserAdapter{}
	default:
		return &GenericAdapter{}
	}
}

// TmuxAdapter captures tmux pane content via `tmux capture-pane`.
// Produces ANSI text content that flows through ANSIToHTML → chromedp → PDF.
type TmuxAdapter struct{}

func (a *TmuxAdapter) CapturePane(pane TmuxPane) (*CaptureResult, error) {
	data, err := captureTmuxPane(pane.PaneID)
	if err != nil {
		return nil, fmt.Errorf("tmux capture: %w", err)
	}

	// Build a synthetic WindowInfo for the rendering pipeline
	win := WindowInfo{
		Owner:    "tmux",
		Name:     pane.Label(),
		OnScreen: true,
	}

	return &CaptureResult{
		Window:      win,
		ContentType: ContentTextANSI,
		Data:        data,
		Title:       pane.Label(),
	}, nil
}

// CmuxAdapter captures cmux surface content via `cmux read-screen`.
// Produces plain text content that flows through html.EscapeString → chromedp → PDF.
type CmuxAdapter struct{}

func (a *CmuxAdapter) CaptureSurface(surface CmuxSurface) (*CaptureResult, error) {
	data, err := captureCmuxSurface(surface.WorkspaceRef, surface.SurfaceRef)
	if err != nil {
		return nil, fmt.Errorf("cmux capture: %w", err)
	}

	win := WindowInfo{
		Owner:    "cmux",
		Name:     surface.Label(),
		OnScreen: true,
	}

	return &CaptureResult{
		Window:      win,
		ContentType: ContentTextPlain,
		Data:        data,
		Title:       surface.Label(),
	}, nil
}

// isGhostty checks if a window owner is Ghostty or cmux (Ghostty multiplexer).
func isGhostty(owner string) bool {
	lower := strings.ToLower(owner)
	return strings.Contains(lower, "ghostty") || strings.Contains(lower, "cmux")
}

// captureWholeWindow captures the entire window as a screenshot.
// Shared fallback used by multiple adapters.
func captureWholeWindow(w WindowInfo) (*CaptureResult, error) {
	data, err := screencaptureWindow(w.ID)
	if err != nil {
		return nil, fmt.Errorf("capture failed for %s: %w", w.Owner, err)
	}

	return &CaptureResult{
		Window:      w,
		ContentType: ContentScreenshot,
		Data:        data,
		Title:       w.Label(),
	}, nil
}

// GhosttyAdapter captures Ghostty windows with split pane detection.
// Falls back to whole-window capture if no splits are found or
// if the Accessibility API is unavailable.
type GhosttyAdapter struct {
	SelectedPanes []int // indices of panes to capture; nil = all
}

func (a *GhosttyAdapter) Capture(w WindowInfo) (*CaptureResult, error) {
	panes, err := detectGhosttyPanes(w)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane detection failed: %v, capturing whole window\n", err)
	}

	// No splits detected — single pane capture
	if len(panes) <= 1 {
		const titleBarHeight = 28
		fullPane := PaneInfo{
			Index:  0,
			X:      0,
			Y:      titleBarHeight,
			Width:  w.Width,
			Height: w.Height - titleBarHeight,
		}

		// Primary: extract full scrollback via Cmd+A, Cmd+C (select_all + copy_to_clipboard)
		// This captures the ENTIRE scrollback buffer, not just the visible viewport.
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Extracting terminal text...\n")
		textData, textErr := extractGhosttyText(w, fullPane)
		if textErr == nil && len(textData) > 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Captured full scrollback (%d bytes)\n", len(textData))
			return &CaptureResult{
				Window:      w,
				ContentType: ContentTextPlain,
				Data:        textData,
				SearchText:  textData,
				Title:       w.Label(),
			}, nil
		}
		if textErr != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Text extraction failed: %v\n", textErr)
		}

		// If active window and text extraction failed, skip scroll-stitch (would hang)
		if w.IsActive {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Active window — falling back to screenshot\n")
			return captureWholeWindow(w)
		}

		// Fallback: scroll-stitch (only for non-active windows)
		searchText, _ := extractGhosttyText(w, fullPane)
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Scroll-capturing %s...\n", w.Owner)
		screenshots, scrollErr := scrollStitchCapture(w, fullPane)
		if scrollErr == nil && len(screenshots) > 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Captured %d page(s)\n", len(screenshots))
			return &CaptureResult{
				Window: w,
				Title:  w.Label(),
				Panes: []PaneCapture{{
					Index:       0,
					ContentType: ContentMultiImage,
					Images:      screenshots,
					SearchText:  searchText,
					Title:       w.Label(),
				}},
			}, nil
		}
		if scrollErr != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Scroll capture failed: %v, falling back to screenshot\n", scrollErr)
		}

		// Fallback: whole window screenshot
		fallback, err := captureWholeWindow(w)
		if err == nil {
			fallback.SearchText = searchText
			return fallback, nil
		}
		if len(searchText) > 0 {
			return &CaptureResult{
				Window:      w,
				ContentType: ContentTextPlain,
				Data:        searchText,
				SearchText:  searchText,
				Title:       w.Label(),
			}, nil
		}
		return nil, err
	}

	// Filter to selected panes if specified
	targetPanes := filterSelectedPanes(panes, a.SelectedPanes)

	// Multi-pane: try write_scrollback_file per pane, fallback to scroll-stitch/screenshot
	var captures []PaneCapture
	var failed int
	for _, pane := range targetPanes {
		// Primary: extract full scrollback via Cmd+A, Cmd+C
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Pane %d: extracting text...\n", pane.Index+1)
		textData, textErr := extractGhosttyText(w, pane)
		if textErr == nil && len(textData) > 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Pane %d: captured scrollback (%d bytes)\n", pane.Index+1, len(textData))
			captures = append(captures, PaneCapture{
				Index:       pane.Index,
				ContentType: ContentTextPlain,
				Data:        textData,
				SearchText:  textData,
				Title:       fmt.Sprintf("%s — pane %d", w.Label(), pane.Index+1),
			})
			continue
		}
		if textErr != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d text extraction failed: %v\n", pane.Index+1, textErr)
		}

		// If active window, skip scroll-stitch
		if w.IsActive {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d: active window — using screenshot\n", pane.Index+1)
			screenX := w.X + pane.X
			screenY := w.Y + pane.Y
			data, err := screencaptureRegion(screenX, screenY, pane.Width, pane.Height)
			if err == nil {
				captures = append(captures, PaneCapture{
					Index:       pane.Index,
					ContentType: ContentScreenshot,
					Data:        data,
					Title:       fmt.Sprintf("%s — pane %d", w.Label(), pane.Index+1),
				})
			} else {
				fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d capture failed: %v\n", pane.Index+1, err)
				failed++
			}
			continue
		}

		searchText, _ := extractGhosttyText(w, pane)

		// Fallback: scroll-stitch (non-active windows only)
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Scroll-capturing pane %d...\n", pane.Index+1)
		screenshots, scrollErr := scrollStitchCapture(w, pane)
		if scrollErr == nil && len(screenshots) > 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Pane %d: %d page(s)\n", pane.Index+1, len(screenshots))
			captures = append(captures, PaneCapture{
				Index:       pane.Index,
				ContentType: ContentMultiImage,
				Images:      screenshots,
				SearchText:  searchText,
				Title:       fmt.Sprintf("%s — pane %d", w.Label(), pane.Index+1),
			})
			continue
		}
		if scrollErr != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d scroll capture failed: %v, using screenshot\n", pane.Index+1, scrollErr)
		}

		// Fallback: single viewport screenshot
		screenX := w.X + pane.X
		screenY := w.Y + pane.Y
		data, err := screencaptureRegion(screenX, screenY, pane.Width, pane.Height)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Pane %d capture failed: %v\n", pane.Index+1, err)
			if len(searchText) > 0 {
				captures = append(captures, PaneCapture{
					Index:       pane.Index,
					ContentType: ContentTextPlain,
					Data:        searchText,
					SearchText:  searchText,
					Title:       fmt.Sprintf("%s — pane %d", w.Label(), pane.Index+1),
				})
				continue
			}
			failed++
			continue
		}
		captures = append(captures, PaneCapture{
			Index:       pane.Index,
			ContentType: ContentScreenshot,
			Data:        data,
			SearchText:  searchText,
			Title:       fmt.Sprintf("%s — pane %d", w.Label(), pane.Index+1),
		})
	}

	// If all pane captures failed, fall back to whole window
	if len(captures) == 0 {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m All pane captures failed, capturing whole window\n")
		return captureWholeWindow(w)
	}

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Captured %d/%d panes (%d failed)\n",
			len(captures), len(targetPanes), failed)
	}

	return &CaptureResult{
		Window: w,
		Title:  w.Label(),
		Panes:  captures,
	}, nil
}

// filterSelectedPanes returns only the panes at the given indices.
// If indices is nil or empty, returns all panes.
func filterSelectedPanes(panes []PaneInfo, indices []int) []PaneInfo {
	if len(indices) == 0 {
		return panes
	}
	sel := make(map[int]bool, len(indices))
	for _, i := range indices {
		sel[i] = true
	}
	var result []PaneInfo
	for _, p := range panes {
		if sel[p.Index] {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return panes
	}
	return result
}

// TerminalAdapter captures terminal windows.
// Terminal.app and iTerm2 support AppleScript text extraction.
// Other terminals fall back to screencapture.
type TerminalAdapter struct{}

func (a *TerminalAdapter) Capture(w WindowInfo) (*CaptureResult, error) {
	lower := strings.ToLower(w.Owner)

	// Terminal.app — AppleScript text extraction
	if strings.Contains(lower, "terminal") && !strings.Contains(lower, "iterm") {
		script := `tell application "Terminal" to get contents of front window`
		data, err := runAppleScript(script)
		if err == nil && len(data) > 0 {
			return &CaptureResult{
				Window:      w,
				ContentType: ContentTextPlain,
				Data:        data,
				Title:       w.Label(),
			}, nil
		}
		// Fall through to screencapture on failure
	}

	// iTerm2 — AppleScript text extraction
	if strings.Contains(lower, "iterm") {
		script := `tell application "iTerm2" to tell current session of current window to get contents`
		data, err := runAppleScript(script)
		if err == nil && len(data) > 0 {
			return &CaptureResult{
				Window:      w,
				ContentType: ContentTextPlain,
				Data:        data,
				Title:       w.Label(),
			}, nil
		}
		// Fall through to screencapture on failure
	}

	return captureWholeWindow(w)
}

// BrowserAdapter captures browser windows via screencapture + URL extraction.
type BrowserAdapter struct{}

func (a *BrowserAdapter) Capture(w WindowInfo) (*CaptureResult, error) {
	data, err := screencaptureWindow(w.ID)
	if err != nil {
		return nil, fmt.Errorf("browser capture failed for %s: %w", w.Owner, err)
	}

	// Try to extract URL for a richer title
	title := w.Label()
	if url := extractBrowserURL(w.Owner); url != "" {
		title = fmt.Sprintf("%s — %s", w.Label(), url)
	}

	return &CaptureResult{
		Window:      w,
		ContentType: ContentScreenshot,
		Data:        data,
		Title:       title,
	}, nil
}

// GenericAdapter captures any window via screencapture.
type GenericAdapter struct{}

func (a *GenericAdapter) Capture(w WindowInfo) (*CaptureResult, error) {
	return captureWholeWindow(w)
}

// screencaptureWindow captures a specific window by its CGWindowID.
func screencaptureWindow(windowID int) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "recap-capture-*.png")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// -l<windowID>: capture specific window by ID
	// -o: no shadow
	// -x: no sound
	cmd := exec.Command("screencapture", "-o", "-x",
		fmt.Sprintf("-l%d", windowID), tmpPath)
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("screencapture failed: %w", err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("screencapture produced empty file")
	}

	return data, nil
}

// extractBrowserURL tries to get the current URL from a browser via AppleScript.
func extractBrowserURL(owner string) string {
	lower := strings.ToLower(owner)

	var script string
	switch {
	case strings.Contains(lower, "safari"):
		script = `tell application "Safari" to get URL of front document`
	case strings.Contains(lower, "chrome"):
		script = `tell application "Google Chrome" to get URL of active tab of front window`
	case strings.Contains(lower, "opera"):
		script = `tell application "Opera" to get URL of active tab of front window`
	case strings.Contains(lower, "arc"):
		script = `tell application "Arc" to get URL of active tab of front window`
	case strings.Contains(lower, "brave"):
		script = `tell application "Brave Browser" to get URL of active tab of front window`
	case strings.Contains(lower, "edge"):
		script = `tell application "Microsoft Edge" to get URL of active tab of front window`
	case strings.Contains(lower, "vivaldi"):
		script = `tell application "Vivaldi" to get URL of active tab of front window`
	default:
		return ""
	}

	data, err := runAppleScript(script)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// runAppleScript executes an AppleScript and returns the output.
func runAppleScript(script string) ([]byte, error) {
	cmd := exec.Command("osascript", "-e", script)
	return cmd.Output()
}

// KittyAdapter captures Kitty pane content via the remote control socket.
// Produces ANSI text content from the pane's scrollback.
type KittyAdapter struct{}

func (a *KittyAdapter) CaptureKittyPane(pane KittyPaneItem) (*CaptureResult, error) {
	data, err := kittyGetText(pane.SocketPath, pane.Pane.ID)
	if err != nil {
		return nil, fmt.Errorf("kitty capture: %w", err)
	}

	title := pane.Pane.Title
	if title == "" {
		title = pane.Pane.ForegroundProcess()
	}
	if pane.Pane.CWD != "" {
		cwd := pane.Pane.CWD
		if home, err := os.UserHomeDir(); err == nil {
			cwd = strings.Replace(cwd, home, "~", 1)
		}
		if title != "" {
			title = fmt.Sprintf("kitty: %s — %s", title, cwd)
		} else {
			title = fmt.Sprintf("kitty: %s", cwd)
		}
	}

	win := WindowInfo{
		Owner:    "kitty",
		Name:     title,
		OnScreen: true,
	}

	return &CaptureResult{
		Window:      win,
		ContentType: ContentTextANSI,
		Data:        data,
		Title:       title,
	}, nil
}
