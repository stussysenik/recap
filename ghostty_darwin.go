//go:build darwin

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GhosttyTab represents a tab discovered from Ghostty's Window menu.
type GhosttyTab struct {
	MenuIndex int    // 1-based index in the Window menu
	RawName   string // Full name including spinner emoji
	Name      string // Name with spinner stripped
}

// stripSpinner removes the leading braille spinner character from Ghostty tab names.
// Ghostty animates tab titles with braille patterns (U+2800-U+28FF) or ✳ (U+2733).
func stripSpinner(name string) string {
	runes := []rune(name)
	if len(runes) >= 2 && runes[1] == ' ' {
		first := runes[0]
		if (first >= 0x2800 && first <= 0x28FF) || first == 0x2733 {
			return string(runes[2:])
		}
	}
	return name
}

// listGhosttyTabs enumerates all Ghostty tabs from the Window menu.
// Uses System Events to read menu items. Tab items appear after the
// "Arrange in Front" menu item in the Window menu.
func listGhosttyTabs() ([]GhosttyTab, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// AppleScript that reads ALL Window menu item names in one atomic call,
	// then returns tab items (after "Arrange in Front") as "index\tname" lines.
	// Getting all names at once avoids the spinner rotation race condition.
	script := `
tell application "System Events"
	tell process "ghostty"
		set allNames to name of every menu item of menu "Window" of menu bar 1
		set c to count of allNames
		set foundSep to false
		set output to ""
		repeat with i from 1 to c
			set n to item i of allNames as text
			if n is "Arrange in Front" then
				set foundSep to true
			else if foundSep and n is not "missing value" and n is not "" then
				set output to output & i & "\t" & n & linefeed
			end if
		end repeat
		return output
	end tell
end tell
`
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list tabs: %w (%s)", err, string(out))
	}

	var tabs []GhosttyTab
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		idx, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		rawName := parts[1]
		if rawName == "(unreadable)" || rawName == "missing value" {
			continue
		}
		tabs = append(tabs, GhosttyTab{
			MenuIndex: idx,
			RawName:   rawName,
			Name:      stripSpinner(rawName),
		})
	}

	return tabs, nil
}

// switchGhosttyTab clicks a Ghostty tab by its Window menu index.
// Uses index-based access to avoid spinner emoji race conditions.
func switchGhosttyTab(menuIndex int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := fmt.Sprintf(`
tell application "System Events"
	tell process "ghostty"
		click menu item %d of menu "Window" of menu bar 1
	end tell
end tell
`, menuIndex)
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("switch tab (index %d): %w (%s)", menuIndex, err, string(out))
	}
	return nil
}

// currentGhosttyTabName returns the name of the currently active Ghostty tab.
func currentGhosttyTabName() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e",
		`tell application "Ghostty" to return name of front window`)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ghosttyConfigPath returns the Ghostty configuration file path on macOS.
func ghosttyConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "com.mitchellh.ghostty", "config")
}

// scrollbackKeybindTag is a comment marker so we can identify injected keybindings.
const scrollbackKeybindTag = "# recap-scrollback-capture (auto-injected, safe to delete)"

// scrollbackKeybind is the keybinding combo used to trigger write_scrollback_file.
// Uses 4 modifiers to avoid conflicting with any user keybinding.
const scrollbackKeybind = "super+ctrl+alt+shift+s"

// findExistingKeybinding scans the Ghostty config for an existing binding to the
// given action. Returns the keybinding string (e.g. "super+shift+s") if found.
func findExistingKeybinding(configPath, action string) (string, bool) {
	f, err := os.Open(configPath)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "keybind") {
			continue
		}
		// Format: keybind = <key>=<action>
		parts := strings.SplitN(line, "=", 2)
		if len(parts) < 2 {
			continue
		}
		val := strings.TrimSpace(parts[1])
		// val is now "<key>=<action>"
		kv := strings.SplitN(val, "=", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[1]) == action {
			return strings.TrimSpace(kv[0]), true
		}
	}
	return "", false
}

// injectKeybinding appends a temporary keybinding to the Ghostty config file.
// Returns a cleanup function that removes the injected line.
func injectKeybinding(configPath, keybind, action string) (func(), error) {
	line := fmt.Sprintf("\n%s\nkeybind = %s=%s\n", scrollbackKeybindTag, keybind, action)

	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open config for append: %w", err)
	}
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return nil, fmt.Errorf("write keybinding: %w", err)
	}
	f.Close()

	cleanup := func() {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return
		}
		// Remove the tag comment and the keybinding line
		lines := strings.Split(string(data), "\n")
		var out []string
		for i := 0; i < len(lines); i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == scrollbackKeybindTag {
				// Skip this line and the next (the keybinding itself)
				if i+1 < len(lines) {
					i++
				}
				continue
			}
			out = append(out, lines[i])
		}
		// Trim trailing empty lines
		for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			out = out[:len(out)-1]
		}
		os.WriteFile(configPath, []byte(strings.Join(out, "\n")+"\n"), 0644)
	}

	return cleanup, nil
}

// sendScrollbackKeybindScript sends the 4-modifier+S keybinding via CGEvent.
const sendScrollbackKeybindScript = `
import CoreGraphics
import Foundation

let pid = pid_t(Int32(CommandLine.arguments[1])!)

let src = CGEventSource(stateID: .hidSystemState)
let down = CGEvent(keyboardEventSource: src, virtualKey: 0x01, keyDown: true)!
let up = CGEvent(keyboardEventSource: src, virtualKey: 0x01, keyDown: false)!
let flags: CGEventFlags = [.maskCommand, .maskControl, .maskAlternate, .maskShift]
down.flags = flags
up.flags = flags
down.postToPid(pid)
up.postToPid(pid)
`

// extractScrollbackFile uses Ghostty's write_scrollback_file action to extract
// the full scrollback buffer as text. This works even from the same window because
// it triggers Ghostty's internal action rather than simulating scroll key events.
func extractScrollbackFile(w WindowInfo, pane PaneInfo) ([]byte, error) {
	configPath := ghosttyConfigPath()
	action := "write_scrollback_file:copy"

	// Check for existing keybinding or inject a temporary one
	existingBind, found := findExistingKeybinding(configPath, action)
	if found {
		fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Found existing keybinding: %s\n", existingBind)
	} else {
		cleanup, err := injectKeybinding(configPath, scrollbackKeybind, action)
		if err != nil {
			return nil, fmt.Errorf("inject keybinding: %w", err)
		}
		defer cleanup()

		// Wait for Ghostty to live-reload config
		time.Sleep(600 * time.Millisecond)
	}

	// Activate window and focus the target pane
	if err := activateWindow(w.PID); err != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Window activation failed: %v (continuing)\n", err)
	}
	time.Sleep(200 * time.Millisecond)

	if pane.Width > 0 && pane.Height > 0 {
		centerX := w.X + pane.X + pane.Width/2
		centerY := w.Y + pane.Y + pane.Height/2
		if err := runClickAt(centerX, centerY); err != nil {
			return nil, fmt.Errorf("focus pane: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Clear clipboard
	clearCmd := exec.Command("bash", "-c", "echo -n | pbcopy")
	clearCmd.Run()
	time.Sleep(100 * time.Millisecond)

	// Send the keybinding to trigger write_scrollback_file:copy
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "swift", "-e", sendScrollbackKeybindScript,
		fmt.Sprintf("%d", w.PID))
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("send keybinding: %w (%s)", err, string(out))
	}

	// Poll clipboard for the temp file path
	var clipContent string
	for i := 0; i < 20; i++ { // up to 4 seconds
		time.Sleep(200 * time.Millisecond)
		out, err := exec.Command("pbpaste").Output()
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(out))
		if content != "" && (strings.HasPrefix(content, "/") && strings.Contains(content, "ghostty")) {
			clipContent = content
			break
		}
	}

	if clipContent == "" {
		return nil, fmt.Errorf("clipboard did not receive scrollback file path (timed out)")
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Scrollback file: %s\n", clipContent)

	// Read the scrollback file
	data, err := os.ReadFile(clipContent)
	if err != nil {
		return nil, fmt.Errorf("read scrollback file %s: %w", clipContent, err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("scrollback file is empty")
	}

	return data, nil
}

// PaneInfo describes a single pane within a Ghostty split layout.
// Coordinates are relative to the window's top-left corner.
type PaneInfo struct {
	Index  int `json:"index"`
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// axPaneScript uses the macOS Accessibility API to detect Ghostty split panes.
// It walks the AX tree looking for split groups and extracts leaf pane bounds.
// Requires Accessibility permission in System Settings.
const axPaneScript = `
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

// Get window position for coordinate conversion
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
    // Convert to window-relative coordinates
    return (Int(Double(point.x) - winX), Int(Double(point.y) - winY), Int(size.width), Int(size.height))
}

func walk(_ el: AXUIElement, depth: Int) {
    let role = getRole(el) ?? ""
    let children = getChildren(el)

    if role == "AXSplitGroup" {
        // Found a split group — collect its direct leaf children
        for child in children {
            let childRole = getRole(child) ?? ""

            if childRole == "AXSplitGroup" {
                // Nested split — recurse deeper
                walk(child, depth: depth + 1)
            } else if childRole == "AXGroup" || childRole == "AXScrollArea" || childRole == "AXTextArea" {
                if let frame = getFrame(child), frame.w >= 50, frame.h >= 50 {
                    panes.append(Pane(x: frame.x, y: frame.y, width: frame.w, height: frame.h))
                }
            }
        }
        return
    }

    // Keep searching deeper
    for child in children {
        walk(child, depth: depth + 1)
    }
}

walk(window, depth: 0)

// Output JSON
var result: [[String: Int]] = []
for (i, p) in panes.enumerated() {
    result.append(["index": i, "x": p.x, "y": p.y, "width": p.width, "height": p.height])
}

let json = try! JSONSerialization.data(withJSONObject: result, options: [])
print(String(data: json, encoding: .utf8)!)
`

// detectGhosttyPanes uses the macOS Accessibility API to find split pane bounds
// in a Ghostty window. Returns nil, nil if no splits are found or if the
// Accessibility permission is not granted (graceful fallback).
func detectGhosttyPanes(w WindowInfo) ([]PaneInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", axPaneScript, fmt.Sprintf("%d", w.PID))
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("pane detection timed out (5s)")
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			// Check for accessibility permission denial
			if isAccessibilityError(stderr) {
				fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Split pane detection needs Accessibility permission.\n")
				fmt.Fprintf(os.Stderr, "        System Settings \u2192 Privacy & Security \u2192 Accessibility \u2192 enable recap\n")
				fmt.Fprintf(os.Stderr, "        Falling back to whole-window capture.\n")
				return nil, nil
			}
		}
		// Non-fatal: fall back to whole-window capture
		return nil, nil
	}

	var panes []PaneInfo
	if err := json.Unmarshal(out, &panes); err != nil {
		return nil, nil
	}

	// Filter out implausibly small panes
	var valid []PaneInfo
	for _, p := range panes {
		if p.Width >= 50 && p.Height >= 50 {
			valid = append(valid, p)
		}
	}

	if len(valid) <= 1 {
		return nil, nil // Single pane = no splits
	}

	return valid, nil
}

// isAccessibilityError checks if a Swift stderr message indicates
// a macOS accessibility permission denial.
func isAccessibilityError(stderr string) bool {
	indicators := []string{
		"kAXErrorAPIDisabled",
		"accessibility",
		"not trusted",
		"AXError",
	}
	for _, s := range indicators {
		if containsIgnoreCase(stderr, s) {
			return true
		}
	}
	return false
}

// containsIgnoreCase does a case-insensitive substring check.
func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// screencaptureRegion captures a specific screen region using screencapture -R.
func screencaptureRegion(x, y, w, h int) ([]byte, error) {
	tmpFile, err := os.CreateTemp("", "recap-pane-*.png")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	region := fmt.Sprintf("%d,%d,%d,%d", x, y, w, h)
	cmd := exec.CommandContext(ctx, "screencapture", "-x", "-R", region, tmpPath)
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("screencapture timed out (5s)")
		}
		return nil, fmt.Errorf("screencapture region failed: %w", err)
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

// countGhosttyPanes returns the number of detected panes for a Ghostty window.
// Returns 0 if detection fails or no splits found.
func countGhosttyPanes(w WindowInfo) int {
	panes, _ := detectGhosttyPanes(w)
	return len(panes)
}

type ghosttyFocusedFrame struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

const axFocusedPaneScript = `
import Cocoa

let pid = Int32(CommandLine.arguments[1])!
let app = AXUIElementCreateApplication(pid)

func getFrame(_ el: AXUIElement) -> (x: Int, y: Int, w: Int, h: Int)? {
    var posRef: CFTypeRef?
    var sizeRef: CFTypeRef?
    guard AXUIElementCopyAttributeValue(el, kAXPositionAttribute as CFString, &posRef) == .success,
          AXUIElementCopyAttributeValue(el, kAXSizeAttribute as CFString, &sizeRef) == .success,
          let pos = posRef as? AXValue,
          let size = sizeRef as? AXValue else {
        return nil
    }

    var point = CGPoint.zero
    var rectSize = CGSize.zero
    AXValueGetValue(pos, .cgPoint, &point)
    AXValueGetValue(size, .cgSize, &rectSize)
    return (Int(point.x), Int(point.y), Int(rectSize.width), Int(rectSize.height))
}

var windowRef: CFTypeRef?
guard AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute as CFString, &windowRef) == .success,
      let window = windowRef as? AXUIElement,
      let windowFrame = getFrame(window) else {
    print("{}")
    exit(0)
}

var focusedRef: CFTypeRef?
guard AXUIElementCopyAttributeValue(app, kAXFocusedUIElementAttribute as CFString, &focusedRef) == .success,
      let focused = focusedRef as? AXUIElement,
      let frame = getFrame(focused) else {
    print("{}")
    exit(0)
}

let result: [String: Int] = [
    "x": frame.x - windowFrame.x,
    "y": frame.y - windowFrame.y,
    "width": frame.w,
    "height": frame.h,
]
let json = try! JSONSerialization.data(withJSONObject: result, options: [])
print(String(data: json, encoding: .utf8)!)
`

// detectActiveGhosttyPane returns the focused Ghostty split index.
// Returns -1 when focus can't be resolved or the window is not split.
func detectActiveGhosttyPane(w WindowInfo) (int, error) {
	panes, err := detectGhosttyPanes(w)
	if err != nil {
		return -1, err
	}
	if len(panes) <= 1 {
		return -1, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", axFocusedPaneScript, fmt.Sprintf("%d", w.PID))
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return -1, fmt.Errorf("focused pane detection timed out (5s)")
		}
		return -1, err
	}

	var frame ghosttyFocusedFrame
	if err := json.Unmarshal(out, &frame); err != nil {
		return -1, err
	}
	if frame.Width <= 0 || frame.Height <= 0 {
		return -1, nil
	}

	cx := frame.X + frame.Width/2
	cy := frame.Y + frame.Height/2
	for _, pane := range panes {
		if cx >= pane.X && cx < pane.X+pane.Width &&
			cy >= pane.Y && cy < pane.Y+pane.Height {
			return pane.Index, nil
		}
	}

	return -1, nil
}

// copyScript uses Cmd+A (select all) + Cmd+C (copy) via CGEvent to extract
// terminal text from a Ghostty window through the macOS clipboard.
// It saves and restores the previous clipboard content.
const copyScript = `
import Cocoa
import CoreGraphics
import Foundation

let pid = pid_t(Int32(CommandLine.arguments[1])!)

// Save current clipboard
let pb = NSPasteboard.general
let saved = pb.string(forType: .string)

// Clear clipboard so we can detect if Cmd+C actually worked
pb.clearContents()

// Send Cmd+A (select all) — key code 0x00 = 'A'
func sendCmd(_ keyCode: CGKeyCode) {
    let src = CGEventSource(stateID: .hidSystemState)
    let down = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: true)!
    let up = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: false)!
    down.flags = .maskCommand
    up.flags = .maskCommand
    down.postToPid(pid)
    up.postToPid(pid)
}

sendCmd(0x00) // Cmd+A
usleep(100_000) // 100ms

sendCmd(0x08) // Cmd+C
usleep(200_000) // 200ms

// Read clipboard
let text = pb.string(forType: .string) ?? ""

// If clipboard is still empty, key events never reached the window
if text.isEmpty {
    pb.clearContents()
    if let saved = saved {
        pb.setString(saved, forType: .string)
    }
    exit(1)
}

// Restore previous clipboard
pb.clearContents()
if let saved = saved {
    pb.setString(saved, forType: .string)
}

// Output the text
print(text)
`

// scrollScript sends keyboard events to a Ghostty process for scrolling.
// Uses CGEvent.postToPid() for key events.
// Key codes: Home=0x73, End=0x77, PageUp=0x74, PageDown=0x79.
// "top" tries Cmd+Home first (instant jump), falls back to 500x Shift+PageUp.
const scrollScript = `
import CoreGraphics
import Foundation

let pid = pid_t(Int32(CommandLine.arguments[1])!)
let action = CommandLine.arguments[2]

func sendKey(_ keyCode: CGKeyCode, flags: CGEventFlags = []) {
    let src = CGEventSource(stateID: .hidSystemState)
    let down = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: true)!
    let up = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: false)!
    if !flags.isEmpty {
        down.flags = flags
        up.flags = flags
    }
    down.postToPid(pid)
    up.postToPid(pid)
}

switch action {
case "top":
    // Try Cmd+Home for instant scroll-to-top
    sendKey(0x73, flags: .maskCommand)
    usleep(50000) // 50ms
    // Fallback: 50x Cmd+PageUp (Ghostty binds super+page_up to scroll_page_up)
    for _ in 0..<50 { sendKey(0x74, flags: .maskCommand); usleep(10000) }
case "pagedown":
    sendKey(0x79, flags: .maskCommand)
case "bottom":
    // Try Cmd+End for instant scroll-to-bottom
    sendKey(0x77, flags: .maskCommand)
    usleep(50000) // 50ms
    // Fallback: 50x Cmd+PageDown (Ghostty binds super+page_down to scroll_page_down)
    for _ in 0..<50 { sendKey(0x79, flags: .maskCommand); usleep(10000) }
default:
    break
}
`

// extractGhosttyText extracts terminal text from a Ghostty window using
// clipboard-based extraction (Cmd+A, Cmd+C). Returns the text content.
func extractGhosttyText(w WindowInfo, pane PaneInfo) ([]byte, error) {
	if data, err := extractGhosttyAXText(w, pane); err == nil && len(data) > 0 {
		return data, nil
	}

	// Activate window so key events reach it
	if err := activateWindow(w.PID); err != nil {
		return nil, fmt.Errorf("activate window: %w", err)
	}
	time.Sleep(200 * time.Millisecond)

	// For multi-pane, click to focus the target pane
	if pane.Index >= 0 && (pane.Width > 0 && pane.Height > 0) {
		centerX := w.X + pane.X + pane.Width/2
		centerY := w.Y + pane.Y + pane.Height/2
		if err := runClickAt(centerX, centerY); err != nil {
			return nil, fmt.Errorf("focus pane: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", copyScript, fmt.Sprintf("%d", w.PID))
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("text extraction timed out (10s)")
		}
		return nil, fmt.Errorf("text extraction failed: %w", err)
	}

	text := strings.TrimRight(string(out), "\n")
	if len(text) == 0 {
		return nil, fmt.Errorf("no text extracted from clipboard")
	}

	// Click pane center to deselect text (less intrusive than Escape for TUI apps)
	if pane.Width > 0 && pane.Height > 0 {
		cx := w.X + pane.X + pane.Width/2
		cy := w.Y + pane.Y + pane.Height/2
		_ = runClickAt(cx, cy) // best-effort
	}

	return []byte(text), nil
}

// activateScript brings a Ghostty window to the foreground using both
// NSRunningApplication.activate() and AX raise. This ensures CGEvent key
// events (Shift+PageUp/Down) actually reach the target window.
const activateScript = `
import Cocoa

let pid = pid_t(Int32(CommandLine.arguments[1])!)
let app = AXUIElementCreateApplication(pid)
var windowsRef: CFTypeRef?
if AXUIElementCopyAttributeValue(app, kAXWindowsAttribute as CFString, &windowsRef) == .success,
   let windows = windowsRef as? [AXUIElement], !windows.isEmpty {
    AXUIElementPerformAction(windows[0], kAXRaiseAction as CFString)
}
if let runningApp = NSRunningApplication(processIdentifier: pid) {
    runningApp.activate(options: [.activateAllWindows, .activateIgnoringOtherApps])
}
`

// clickScriptTmpl is a Swift snippet for sending a mouse click.
// The coordinates are interpolated via fmt.Sprintf to avoid negative-number
// CLI argument issues with `swift -e`.
const clickScriptTmpl = `
import CoreGraphics
import Foundation

let point = CGPoint(x: %d.0, y: %d.0)
let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left)!
let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left)!
down.post(tap: .cghidEventTap)
up.post(tap: .cghidEventTap)
`

// activateWindow brings the Ghostty window for the given PID to the foreground.
func activateWindow(pid int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", activateScript,
		fmt.Sprintf("%d", pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("activate window failed: %w (%s)", err, string(out))
	}
	return nil
}

// runClickAt sends a mouse click at the given screen coordinates.
func runClickAt(x, y int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := fmt.Sprintf(clickScriptTmpl, x, y)
	cmd := exec.CommandContext(ctx, "swift", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("click failed: %w (%s)", err, string(out))
	}
	return nil
}

// runScrollAction sends a scroll action to a Ghostty process.
func runScrollAction(pid int, action string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", scrollScript,
		fmt.Sprintf("%d", pid), action)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scroll %s failed: %w (%s)", action, err, string(out))
	}
	return nil
}

// imageHash decodes a PNG and returns a SHA-256 hash of its pixel data.
// This ignores PNG metadata/compression differences, detecting truly identical images.
func imageHash(data []byte) [32]byte {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		// Fallback: hash the raw bytes
		return sha256.Sum256(data)
	}
	b := img.Bounds()
	h := sha256.New()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			h.Write([]byte{byte(r >> 8), byte(g >> 8), byte(bl >> 8), byte(a >> 8)})
		}
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// isBlankImage checks if a PNG is a solid single-color image (e.g. desktop wallpaper).
// It samples ~100 pixels across the image and checks if they all match the first pixel
// within a small tolerance.
func isBlankImage(data []byte) bool {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return false
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return true
	}

	// Get reference color from first pixel
	refR, refG, refB, _ := img.At(b.Min.X, b.Min.Y).RGBA()

	// Sample ~100 pixels spread across the image
	steps := 10
	dx := w / steps
	dy := h / steps
	if dx < 1 {
		dx = 1
	}
	if dy < 1 {
		dy = 1
	}

	const tolerance = 0x0A00 // ~4% of 16-bit color range
	for sy := b.Min.Y; sy < b.Max.Y; sy += dy {
		for sx := b.Min.X; sx < b.Max.X; sx += dx {
			r, g, bl, _ := img.At(sx, sy).RGBA()
			if diff(r, refR) > tolerance || diff(g, refG) > tolerance || diff(bl, refB) > tolerance {
				return false
			}
		}
	}
	return true
}

// diff returns the absolute difference between two uint32 values.
func diff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// scrollKeyScript sends a Cmd+Key event to a process via CGEvent.
// Used for Cmd+Home (scroll to top), Cmd+End (scroll to bottom),
// and Cmd+PageDown (scroll one viewport).
const scrollKeyScript = `
import Cocoa
import CoreGraphics

let pid = pid_t(Int32(CommandLine.arguments[1])!)
let keyCode = CGKeyCode(UInt16(CommandLine.arguments[2])!)

let src = CGEventSource(stateID: .hidSystemState)
let down = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: true)!
let up = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: false)!
down.flags = [.maskCommand, .maskNumericPad, .maskSecondaryFn]
up.flags = [.maskCommand, .maskNumericPad, .maskSecondaryFn]
down.postToPid(pid)
up.postToPid(pid)
`

// Key codes for scroll navigation
const (
	keyCodeHome     = 0x73
	keyCodeEnd      = 0x77
	keyCodePageDown = 0x79
)

// sendScrollKey sends a Cmd+Key event to a Ghostty process.
func sendScrollKey(pid int, keyCode int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", scrollKeyScript,
		fmt.Sprintf("%d", pid), fmt.Sprintf("%d", keyCode))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sendScrollKey failed: %w (%s)", err, string(out))
	}
	return nil
}

// rowHash returns a SHA-256 hash of all RGBA pixel values in a single row.
func rowHash(img image.Image, y int) [32]byte {
	b := img.Bounds()
	h := sha256.New()
	for x := b.Min.X; x < b.Max.X; x++ {
		r, g, bl, a := img.At(x, y).RGBA()
		h.Write([]byte{byte(r >> 8), byte(g >> 8), byte(bl >> 8), byte(a >> 8)})
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

// subImager is implemented by all standard library image types and provides
// zero-copy cropping via SubImage.
type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

// copyPHYs finds the pHYs chunk in original and splices it into reencoded.
// PNG chunks are: 4-byte length (big-endian) + 4-byte type + data + 4-byte CRC.
// The pHYs chunk is inserted right after IHDR (before any IDAT).
func copyPHYs(original, reencoded []byte) []byte {
	// PNG signature is 8 bytes
	const sigLen = 8

	// Find pHYs chunk in original
	var phys []byte
	for pos := sigLen; pos+8 <= len(original); {
		chunkLen := int(binary.BigEndian.Uint32(original[pos : pos+4]))
		chunkType := string(original[pos+4 : pos+8])
		totalLen := 4 + 4 + chunkLen + 4 // length + type + data + crc
		if pos+totalLen > len(original) {
			break
		}
		if chunkType == "pHYs" {
			phys = original[pos : pos+totalLen]
			break
		}
		pos += totalLen
	}
	if phys == nil {
		return reencoded
	}

	// Find insertion point in reencoded: right after IHDR
	pos := sigLen
	if pos+8 > len(reencoded) {
		return reencoded
	}
	ihdrLen := int(binary.BigEndian.Uint32(reencoded[pos : pos+4]))
	ihdrEnd := pos + 4 + 4 + ihdrLen + 4
	if ihdrEnd > len(reencoded) {
		return reencoded
	}

	// Build new PNG: signature + IHDR + pHYs + rest
	var out bytes.Buffer
	out.Grow(len(reencoded) + len(phys))
	out.Write(reencoded[:ihdrEnd])
	out.Write(phys)
	out.Write(reencoded[ihdrEnd:])
	return out.Bytes()
}

// trimOverlap detects overlapping pixel rows between consecutive screenshots
// and crops them out so the stitched result has no repeated content.
func trimOverlap(images [][]byte) ([][]byte, error) {
	if len(images) <= 1 {
		return images, nil
	}

	result := [][]byte{images[0]}

	for i := 1; i < len(images); i++ {
		imgA, err := png.Decode(bytes.NewReader(images[i-1]))
		if err != nil {
			result = append(result, images[i])
			continue
		}
		imgB, err := png.Decode(bytes.NewReader(images[i]))
		if err != nil {
			result = append(result, images[i])
			continue
		}

		boundsA := imgA.Bounds()
		boundsB := imgB.Bounds()
		heightA := boundsA.Dy()
		heightB := boundsB.Dy()

		// Hash bottom half of A
		startA := heightA / 2
		hashesA := make([][32]byte, heightA-startA)
		for y := startA; y < heightA; y++ {
			hashesA[y-startA] = rowHash(imgA, boundsA.Min.Y+y)
		}

		// Hash top half of B
		endB := heightB / 2
		hashesB := make([][32]byte, endB)
		for y := 0; y < endB; y++ {
			hashesB[y] = rowHash(imgB, boundsB.Min.Y+y)
		}

		// Search for largest overlap (last N rows of A == first N rows of B)
		maxOverlap := len(hashesA)
		if len(hashesB) < maxOverlap {
			maxOverlap = len(hashesB)
		}

		overlap := 0
		for n := maxOverlap; n >= 10; n-- {
			match := true
			for k := 0; k < n; k++ {
				if hashesA[len(hashesA)-n+k] != hashesB[k] {
					match = false
					break
				}
			}
			if match {
				overlap = n
				break
			}
		}

		if overlap > 0 {
			// Crop top `overlap` rows from B
			si, ok := imgB.(subImager)
			if !ok {
				result = append(result, images[i])
				continue
			}
			cropped := si.SubImage(image.Rect(
				boundsB.Min.X, boundsB.Min.Y+overlap,
				boundsB.Max.X, boundsB.Max.Y,
			))
			var buf bytes.Buffer
			if err := png.Encode(&buf, cropped); err != nil {
				result = append(result, images[i])
				continue
			}
			// Preserve DPI metadata (pHYs chunk) from the original PNG
			result = append(result, copyPHYs(images[i], buf.Bytes()))
		} else {
			result = append(result, images[i])
		}
	}

	return result, nil
}

// trimBottomPadding removes a large trailing run of uniform background rows
// from the final stitched screenshot so the output ends at the last real line.
func trimBottomPadding(images [][]byte) [][]byte {
	if len(images) == 0 {
		return images
	}

	result := append([][]byte(nil), images...)
	last := len(result) - 1
	if cropped, err := cropBottomBlankRows(result[last]); err == nil {
		result[last] = cropped
	}
	return result
}

// cropBottomBlankRows trims trailing rows that are effectively identical to the
// bottom-left background color. This targets the empty terminal area that
// appears after reaching the end of scrollback on the final capture.
func cropBottomBlankRows(data []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return data, nil
	}

	refR, refG, refB, refA := img.At(b.Min.X, b.Max.Y-1).RGBA()
	trimTop := b.Max.Y
	blankRows := 0

	for y := b.Max.Y - 1; y >= b.Min.Y; y-- {
		if !rowMatchesColor(img, y, refR, refG, refB, refA) {
			break
		}
		trimTop = y
		blankRows++
	}

	const minBlankRows = 12
	if blankRows < minBlankRows || trimTop <= b.Min.Y {
		return data, nil
	}

	si, ok := img.(subImager)
	if !ok {
		return data, nil
	}

	cropped := si.SubImage(image.Rect(b.Min.X, b.Min.Y, b.Max.X, trimTop))
	var buf bytes.Buffer
	if err := png.Encode(&buf, cropped); err != nil {
		return data, nil
	}

	return copyPHYs(data, buf.Bytes()), nil
}

func rowMatchesColor(img image.Image, y int, refR, refG, refB, refA uint32) bool {
	b := img.Bounds()
	maxMismatches := b.Dx() / 200
	if maxMismatches < 2 {
		maxMismatches = 2
	}

	const tolerance = 0x0800
	mismatches := 0
	for x := b.Min.X; x < b.Max.X; x++ {
		r, g, bl, a := img.At(x, y).RGBA()
		if diff(r, refR) > tolerance ||
			diff(g, refG) > tolerance ||
			diff(bl, refB) > tolerance ||
			diff(a, refA) > tolerance {
			mismatches++
			if mismatches > maxMismatches {
				return false
			}
		}
	}
	return true
}

// scrollStitchCapture captures the full scrollback of a pane by scrolling
// through it and taking screenshots at each page. Returns ordered PNGs.
func scrollStitchCapture(w WindowInfo, pane PaneInfo) ([][]byte, error) {
	// Activate the Ghostty window (bring to front so key events reach it)
	if err := activateWindow(w.PID); err != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Window activation failed: %v (continuing anyway)\n", err)
	}
	time.Sleep(200 * time.Millisecond)

	screenX := w.X + pane.X
	screenY := w.Y + pane.Y

	// Click to focus the pane
	centerX := screenX + pane.Width/2
	centerY := screenY + pane.Height/2
	if err := runClickAt(centerX, centerY); err != nil {
		return nil, fmt.Errorf("focus pane: %w", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Scroll to top
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Scrolling pane %d to top...\n", pane.Index+1)
	if err := runScrollAction(w.PID, "top"); err != nil {
		return nil, fmt.Errorf("scroll to top: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Stability check: take screenshots until 2 consecutive frames match
	// This confirms rendering has settled after scroll-to-top.
	var stableHash [sha256.Size]byte
	for stabilityCheck := 0; stabilityCheck < 10; stabilityCheck++ {
		data, err := screencaptureRegion(screenX, screenY, pane.Width, pane.Height)
		if err != nil {
			break
		}
		hash := sha256.Sum256(data)
		if stabilityCheck > 0 && hash == stableHash {
			break // rendering settled
		}
		stableHash = hash
		time.Sleep(200 * time.Millisecond)
	}

	// Overall timeout to prevent hanging
	deadline := time.Now().Add(60 * time.Second)

	// Capture loop
	var screenshots [][]byte
	var prevHash [sha256.Size]byte
	var matchCount int // consecutive identical hash count
	maxPages := 200    // safety limit

	for i := 0; i < maxPages; i++ {
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Scroll capture timed out after 60s (%d pages captured)\n", len(screenshots))
			break
		}
		if i > 0 && (i+1)%5 == 0 {
			fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m   Captured page %d...\n", i+1)
		}
		data, err := screencaptureRegion(screenX, screenY, pane.Width, pane.Height)
		if err != nil {
			if len(screenshots) > 0 {
				break // partial capture is okay
			}
			return nil, fmt.Errorf("capture page %d: %w", i+1, err)
		}

		// Skip blank/solid-color frames (desktop wallpaper, transition artifacts)
		if isBlankImage(data) {
			continue
		}

		// Hash to detect duplicate (reached bottom)
		// Require 2 consecutive identical hashes to prevent premature stop
		// from render glitches or partial frame captures.
		hash := sha256.Sum256(data)
		if i > 0 && hash == prevHash {
			matchCount++
			if matchCount >= 2 {
				break // 2 consecutive identical screenshots — confirmed at bottom
			}
			time.Sleep(300 * time.Millisecond)
			continue
		}
		matchCount = 0
		prevHash = hash

		screenshots = append(screenshots, data)

		if i < maxPages-1 {
			if err := runScrollAction(w.PID, "pagedown"); err != nil {
				break // can't scroll further
			}
			time.Sleep(300 * time.Millisecond)
		}
	}

	// Restore scroll position (scroll back to bottom)
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Restoring scroll position...\n")
	_ = runScrollAction(w.PID, "bottom")

	if len(screenshots) == 0 {
		return nil, fmt.Errorf("no screenshots captured")
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Captured %d page(s) for pane %d\n", len(screenshots), pane.Index+1)

	trimmed, err := trimOverlap(screenshots)
	if err != nil {
		return nil, err
	}

	return trimBottomPadding(trimmed), nil
}
