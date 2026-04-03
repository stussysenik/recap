//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	ghosttyFieldSep  = "\x1f"
	ghosttyRecordSep = "\x1e"
)

var ghosttyClipboardMu sync.Mutex

// GhosttyTerminal describes a single terminal surface in the selected tab
// of Ghostty's front window. Ghostty's AppleScript model exposes these as
// stable objects that can receive actions without focus-stealing input.
type GhosttyTerminal struct {
	ID               string
	Name             string
	WorkingDirectory string
	Focused          bool
}

func ghosttyTerminalDisplayName(term GhosttyTerminal) string {
	name := strings.TrimSpace(stripSpinner(term.Name))
	if name != "" {
		return name
	}
	cwd := strings.TrimSpace(term.WorkingDirectory)
	if cwd != "" {
		return cwd
	}
	return term.ID
}

// listGhosttySelectedTabTerminals returns the terminals in Ghostty's selected
// tab for the app's front window. The ordering matches Ghostty's surface-tree
// leaf order, which is also how Ghostty's AppleScript layer enumerates panes.
func listGhosttySelectedTabTerminals() ([]GhosttyTerminal, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := `
set fieldSep to ASCII character 31
set recordSep to ASCII character 30

tell application "Ghostty"
	if (count of windows) is 0 then
		return ""
	end if

	set win to front window
	set focusedID to ""
	try
		set focusedID to id of focused terminal of selected tab of win
	end try

	set output to (name of win) & recordSep
	repeat with t in terminals of selected tab of win
		set termID to id of t
		set termName to ""
		set termCWD to ""
		set isFocused to "0"

		try
			set termName to name of t
		end try
		try
			set termCWD to working directory of t
		end try
		if focusedID is termID then
			set isFocused to "1"
		end if

		set output to output & termID & fieldSep & termName & fieldSep & termCWD & fieldSep & isFocused & recordSep
	end repeat
	return output
end tell
`

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, "", fmt.Errorf("list Ghostty terminals: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	terminals, windowTitle := parseGhosttySelectedTabTerminals(string(out))
	if len(terminals) == 0 {
		return nil, windowTitle, fmt.Errorf("no Ghostty terminals found in selected tab")
	}
	return terminals, windowTitle, nil
}

func parseGhosttySelectedTabTerminals(raw string) ([]GhosttyTerminal, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}

	records := strings.Split(raw, ghosttyRecordSep)
	if len(records) == 0 {
		return nil, ""
	}

	windowTitle := strings.TrimSpace(records[0])
	var terminals []GhosttyTerminal

	for _, record := range records[1:] {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		fields := strings.Split(record, ghosttyFieldSep)
		if len(fields) < 4 {
			continue
		}
		terminals = append(terminals, GhosttyTerminal{
			ID:               strings.TrimSpace(fields[0]),
			Name:             strings.TrimSpace(fields[1]),
			WorkingDirectory: strings.TrimSpace(fields[2]),
			Focused:          strings.TrimSpace(fields[3]) == "1",
		})
	}

	return terminals, windowTitle
}

func findGhosttyTerminalMatches(terminals []GhosttyTerminal, filter string) []GhosttyTerminal {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return nil
	}

	var matches []GhosttyTerminal
	for _, term := range terminals {
		name := strings.ToLower(stripSpinner(term.Name))
		cwd := strings.ToLower(term.WorkingDirectory)
		if strings.Contains(name, filter) || strings.Contains(cwd, filter) {
			matches = append(matches, term)
		}
	}
	return matches
}

func frontGhosttyFocusedTerminal() (*GhosttyTerminal, string, error) {
	terminals, windowTitle, err := listGhosttySelectedTabTerminals()
	if err != nil {
		return nil, windowTitle, err
	}
	for i := range terminals {
		if terminals[i].Focused {
			return &terminals[i], windowTitle, nil
		}
	}
	if len(terminals) == 1 {
		return &terminals[0], windowTitle, nil
	}
	return nil, windowTitle, fmt.Errorf("no focused Ghostty terminal found")
}

func isFrontGhosttyWindow(w WindowInfo) bool {
	current := strings.TrimSpace(stripSpinner(currentGhosttyTabName()))
	if current == "" {
		return false
	}
	name := strings.TrimSpace(stripSpinner(w.Name))
	return strings.EqualFold(current, name)
}

func readClipboardText() string {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func writeClipboardText(text string) {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}

func readGhosttyScrollbackPathFromClipboard(marker string) (string, error) {
	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		content := strings.TrimSpace(readClipboardText())
		if content == "" || content == marker {
			continue
		}
		if strings.HasPrefix(content, "/") && strings.Contains(content, "history") {
			return content, nil
		}
	}
	return "", fmt.Errorf("clipboard did not receive scrollback file path (timed out)")
}

// extractGhosttyTerminalScrollback triggers Ghostty's native scrollback export
// on an exact terminal object via AppleScript. This avoids focus, cursor, and
// keybinding injection entirely; only the clipboard is touched briefly and then
// restored.
func extractGhosttyTerminalScrollback(term GhosttyTerminal) ([]byte, error) {
	ghosttyClipboardMu.Lock()
	defer ghosttyClipboardMu.Unlock()

	previousClipboard := readClipboardText()
	defer writeClipboardText(previousClipboard)

	marker := fmt.Sprintf("__recap_ghostty_%d__", time.Now().UnixNano())
	writeClipboardText(marker)
	time.Sleep(80 * time.Millisecond)

	script := fmt.Sprintf(`tell application "Ghostty" to perform action "write_scrollback_file:copy" on terminal id %q`, term.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("perform Ghostty scrollback export: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	path, err := readGhosttyScrollbackPathFromClipboard(marker)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scrollback file %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("scrollback file is empty")
	}
	return data, nil
}

func captureAndRenderGhosttyTerminal(term GhosttyTerminal, windowTitle, format, outputBase string) ([]string, error) {
	data, err := extractGhosttyTerminalScrollback(term)
	if err != nil {
		return nil, err
	}

	if windowTitle == "" {
		windowTitle = ghosttyTerminalDisplayName(term)
	}

	result := &CaptureResult{
		Window: WindowInfo{
			Owner:    "ghostty",
			Name:     windowTitle,
			OnScreen: true,
		},
		ContentType: ContentTextPlain,
		Data:        data,
		SearchText:  data,
		Title:       fmt.Sprintf("ghostty — %s", ghosttyTerminalDisplayName(term)),
	}

	path, err := renderSingle(result, format, outputBase, "")
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

type GhosttyAXTextPane struct {
	Index  int    `json:"index"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Text   string `json:"text"`
}

const ghosttyAXTextScript = `
import Cocoa

let pid = Int32(CommandLine.arguments[1])!
let app = AXUIElementCreateApplication(pid)

var windowsRef: CFTypeRef?
guard AXUIElementCopyAttributeValue(app, kAXWindowsAttribute as CFString, &windowsRef) == .success,
      let windows = windowsRef as? [AXUIElement],
      !windows.isEmpty else {
    print("[]")
    exit(0)
}

let window = windows[0]

var posRef: CFTypeRef?
var winX: Double = 0
var winY: Double = 0
if AXUIElementCopyAttributeValue(window, kAXPositionAttribute as CFString, &posRef) == .success {
    var point = CGPoint.zero
    AXValueGetValue(posRef as! AXValue, .cgPoint, &point)
    winX = Double(point.x)
    winY = Double(point.y)
}

struct Pane {
    var x: Int
    var y: Int
    var width: Int
    var height: Int
    var text: String
}

var panes: [Pane] = []

func getRole(_ el: AXUIElement) -> String? {
    var roleRef: CFTypeRef?
    guard AXUIElementCopyAttributeValue(el, kAXRoleAttribute as CFString, &roleRef) == .success else {
        return nil
    }
    return roleRef as? String
}

func getChildren(_ el: AXUIElement) -> [AXUIElement] {
    var childrenRef: CFTypeRef?
    guard AXUIElementCopyAttributeValue(el, kAXChildrenAttribute as CFString, &childrenRef) == .success,
          let children = childrenRef as? [AXUIElement] else {
        return []
    }
    return children
}

func getFrame(_ el: AXUIElement) -> (x: Int, y: Int, w: Int, h: Int)? {
    var posRef: CFTypeRef?
    var sizeRef: CFTypeRef?
    guard AXUIElementCopyAttributeValue(el, kAXPositionAttribute as CFString, &posRef) == .success,
          AXUIElementCopyAttributeValue(el, kAXSizeAttribute as CFString, &sizeRef) == .success else {
        return nil
    }
    var point = CGPoint.zero
    var size = CGSize.zero
    AXValueGetValue(posRef as! AXValue, .cgPoint, &point)
    AXValueGetValue(sizeRef as! AXValue, .cgSize, &size)
    return (Int(Double(point.x) - winX), Int(Double(point.y) - winY), Int(size.width), Int(size.height))
}

func getValue(_ el: AXUIElement) -> String {
    var valueRef: CFTypeRef?
    guard AXUIElementCopyAttributeValue(el, kAXValueAttribute as CFString, &valueRef) == .success,
          let value = valueRef as? String else {
        return ""
    }
    return value
}

func walk(_ el: AXUIElement) {
    let role = getRole(el) ?? ""
    if role == "AXTextArea" {
        if let frame = getFrame(el), frame.w >= 50, frame.h >= 50 {
            panes.append(Pane(x: frame.x, y: frame.y, width: frame.w, height: frame.h, text: getValue(el)))
        }
    }

    for child in getChildren(el) {
        walk(child)
    }
}

walk(window)

var result: [[String: Any]] = []
for (i, p) in panes.enumerated() {
    result.append([
        "index": i,
        "x": p.x,
        "y": p.y,
        "width": p.width,
        "height": p.height,
        "text": p.text,
    ])
}

let json = try! JSONSerialization.data(withJSONObject: result, options: [])
print(String(data: json, encoding: .utf8)!)
`

func detectGhosttyTextPanes(w WindowInfo) ([]GhosttyAXTextPane, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", ghosttyAXTextScript, fmt.Sprintf("%d", w.PID))
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("Ghostty Accessibility text timed out (5s)")
		}
		if exitErr, ok := err.(*exec.ExitError); ok && isAccessibilityError(string(exitErr.Stderr)) {
			return nil, fmt.Errorf("Ghostty Accessibility text needs Accessibility permission")
		}
		return nil, fmt.Errorf("Ghostty Accessibility text: %w", err)
	}

	var panes []GhosttyAXTextPane
	if err := json.Unmarshal(out, &panes); err != nil {
		return nil, fmt.Errorf("decode Ghostty Accessibility text: %w", err)
	}
	return panes, nil
}

func extractGhosttyAXText(w WindowInfo, pane PaneInfo) ([]byte, error) {
	panes, err := detectGhosttyTextPanes(w)
	if err != nil {
		return nil, err
	}
	if len(panes) == 0 {
		return nil, fmt.Errorf("no Ghostty Accessibility text areas found")
	}

	if pane.Index >= 0 && pane.Index < len(panes) {
		if text := panes[pane.Index].Text; text != "" {
			return []byte(text), nil
		}
	}
	if len(panes) == 1 && panes[0].Text != "" {
		return []byte(panes[0].Text), nil
	}

	return nil, fmt.Errorf("no Accessibility text for pane %d", pane.Index+1)
}
