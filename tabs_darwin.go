//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// AXTab describes a single tab discovered via the macOS Accessibility API.
// Works for any terminal emulator that exposes tabs as AXTabGroup elements
// (iTerm2, Terminal.app, and others that follow macOS AX conventions).
type AXTab struct {
	Window int    `json:"window"` // window index within the app
	Tab    int    `json:"tab"`    // tab index within the window
	Title  string `json:"title"`  // tab title (e.g. "zsh", "nvim — project")
	Active bool   `json:"active"` // whether this tab is currently selected
}

// axTabScript walks the Accessibility tree for a given PID to find
// AXTabGroup elements and enumerate their AXTab children. This is the
// same API pattern used in ghostty_darwin.go for AXSplitGroup, extended
// to handle the tab hierarchy that most terminal emulators expose.
//
// Returns JSON array of tab objects with window index, tab index, title,
// and active state.
const axTabScript = `
import Cocoa

let pid = pid_t(Int32(CommandLine.arguments[1])!)
let app = AXUIElementCreateApplication(pid)

var windowsRef: CFTypeRef?
AXUIElementCopyAttributeValue(app, kAXWindowsAttribute as CFString, &windowsRef)
guard let windows = windowsRef as? [AXUIElement] else { print("[]"); exit(0) }

var result: [[String: Any]] = []

for (wIdx, window) in windows.enumerated() {
    // Recursively find all AXTabGroup elements in this window's AX tree
    func findTabGroups(_ el: AXUIElement) -> [AXUIElement] {
        var role: CFTypeRef?
        AXUIElementCopyAttributeValue(el, kAXRoleAttribute as CFString, &role)
        if (role as? String) == "AXTabGroup" { return [el] }

        var children: CFTypeRef?
        AXUIElementCopyAttributeValue(el, kAXChildrenAttribute as CFString, &children)
        guard let kids = children as? [AXUIElement] else { return [] }
        return kids.flatMap { findTabGroups($0) }
    }

    for tabGroup in findTabGroups(window) {
        var tabsRef: CFTypeRef?
        AXUIElementCopyAttributeValue(tabGroup, kAXTabsAttribute as CFString, &tabsRef)
        guard let tabs = tabsRef as? [AXUIElement] else { continue }

        // Get the currently selected tab via AXValue
        var selectedRef: CFTypeRef?
        AXUIElementCopyAttributeValue(tabGroup, kAXValueAttribute as CFString, &selectedRef)

        for (tIdx, tab) in tabs.enumerated() {
            var titleRef: CFTypeRef?
            AXUIElementCopyAttributeValue(tab, kAXTitleAttribute as CFString, &titleRef)
            let title = (titleRef as? String) ?? ""

            // Check if this tab is the selected one by comparing AXUIElement refs
            var isSelected = false
            if let sel = selectedRef {
                isSelected = CFEqual(sel as CFTypeRef, tab as CFTypeRef)
            }

            result.append([
                "window": wIdx,
                "tab": tIdx,
                "title": title,
                "active": isSelected,
            ])
        }
    }
}

let json = try! JSONSerialization.data(withJSONObject: result)
print(String(data: json, encoding: .utf8)!)
`

// detectTabs uses the macOS Accessibility API to enumerate tabs for a
// terminal window. Returns nil if no AXTabGroup is found (terminal doesn't
// expose tabs via AX), or if Accessibility permission is denied.
//
// This works for any terminal emulator that follows macOS AX conventions:
// iTerm2, Terminal.app, and others. Ghostty and Kitty may not expose tabs
// this way, but they have their own detection paths.
func detectTabs(w WindowInfo) ([]AXTab, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", axTabScript, fmt.Sprintf("%d", w.PID))
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("tab detection timed out (5s)")
		}
		// Non-fatal: terminal may not expose tabs via AX
		return nil, nil
	}

	var tabs []AXTab
	if err := json.Unmarshal(out, &tabs); err != nil {
		return nil, nil
	}

	// Only return tabs if we found more than one — a single tab is the same
	// as the window itself, no need to decompose.
	if len(tabs) <= 1 {
		return nil, nil
	}

	return tabs, nil
}

// countTabs returns the number of AX-detected tabs for a window.
// Returns 0 if detection fails or only 1 tab found.
func countTabs(w WindowInfo) int {
	tabs, _ := detectTabs(w)
	return len(tabs)
}
