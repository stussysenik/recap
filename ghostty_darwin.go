//go:build darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

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

// scrollScript sends keyboard events to a Ghostty process for scrolling.
// Uses CGEvent.postToPid() to send Shift+PageUp/Down for scrollback navigation.
// Key codes: PageUp=0x74, PageDown=0x79.
const scrollScript = `
import CoreGraphics
import Foundation

let pid = pid_t(Int32(CommandLine.arguments[1])!)
let action = CommandLine.arguments[2]

func sendKey(_ keyCode: CGKeyCode, shift: Bool) {
    let src = CGEventSource(stateID: .hidSystemState)
    let down = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: true)!
    let up = CGEvent(keyboardEventSource: src, virtualKey: keyCode, keyDown: false)!
    if shift {
        down.flags = .maskShift
        up.flags = .maskShift
    }
    down.postToPid(pid)
    up.postToPid(pid)
}

switch action {
case "top":
    for _ in 0..<300 { sendKey(0x74, shift: true); usleep(3000) }
case "pagedown":
    sendKey(0x79, shift: true)
case "bottom":
    for _ in 0..<300 { sendKey(0x79, shift: true); usleep(3000) }
default:
    break
}
`

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

// clickScript sends a mouse click to focus a specific pane via CGEvent.
const clickScript = `
import CoreGraphics
import Foundation

let x = Double(CommandLine.arguments[1])!
let y = Double(CommandLine.arguments[2])!
let point = CGPoint(x: x, y: y)
let down = CGEvent(mouseEventSource: nil, mouseType: .leftMouseDown, mouseCursorPosition: point, mouseButton: .left)!
let up = CGEvent(mouseEventSource: nil, mouseType: .leftMouseUp, mouseCursorPosition: point, mouseButton: .left)!
down.post(tap: .cghidEventTap)
up.post(tap: .cghidEventTap)
`

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

	cmd := exec.CommandContext(ctx, "swift", "-e", clickScript,
		fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("click failed: %w (%s)", err, string(out))
	}
	return nil
}

// scrollStitchCapture captures the full scrollback of a pane by scrolling
// through it and taking screenshots at each page. Returns ordered PNGs.
func scrollStitchCapture(w WindowInfo, pane PaneInfo) ([][]byte, error) {
	// 0. Activate the Ghostty window (bring to front so key events reach it)
	if err := activateWindow(w.PID); err != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Window activation failed: %v (continuing anyway)\n", err)
	}
	time.Sleep(200 * time.Millisecond)

	screenX := w.X + pane.X
	screenY := w.Y + pane.Y

	// 1. Click to focus the pane
	centerX := screenX + pane.Width/2
	centerY := screenY + pane.Height/2
	if err := runClickAt(centerX, centerY); err != nil {
		return nil, fmt.Errorf("focus pane: %w", err)
	}
	time.Sleep(100 * time.Millisecond)

	// 2. Scroll to top
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Scrolling pane %d to top...\n", pane.Index+1)
	if err := runScrollAction(w.PID, "top"); err != nil {
		return nil, fmt.Errorf("scroll to top: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	// 3. Capture loop
	var screenshots [][]byte
	var prevHash [sha256.Size]byte
	maxPages := 200 // safety limit

	for i := 0; i < maxPages; i++ {
		data, err := screencaptureRegion(screenX, screenY, pane.Width, pane.Height)
		if err != nil {
			if len(screenshots) > 0 {
				break // partial capture is okay
			}
			return nil, fmt.Errorf("capture page %d: %w", i+1, err)
		}

		// Hash to detect duplicate (reached bottom)
		hash := sha256.Sum256(data)
		if i > 0 && hash == prevHash {
			break // same as previous screenshot — we're at the bottom
		}
		prevHash = hash

		screenshots = append(screenshots, data)

		if i < maxPages-1 {
			if err := runScrollAction(w.PID, "pagedown"); err != nil {
				break // can't scroll further
			}
			time.Sleep(150 * time.Millisecond)
		}
	}

	// 4. Restore scroll position (scroll back to bottom)
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Restoring scroll position...\n")
	_ = runScrollAction(w.PID, "bottom")

	if len(screenshots) == 0 {
		return nil, fmt.Errorf("no screenshots captured")
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Captured %d page(s) for pane %d\n", len(screenshots), pane.Index+1)
	return screenshots, nil
}
