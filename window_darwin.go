//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// AppType classifies a window's application.
type AppType int

const (
	AppTerminal AppType = iota
	AppBrowser
	AppGeneric
)

func (t AppType) String() string {
	switch t {
	case AppTerminal:
		return "Terminal"
	case AppBrowser:
		return "Browser"
	default:
		return "Desktop"
	}
}

// WindowInfo describes a visible window on screen.
type WindowInfo struct {
	ID     int     `json:"id"`
	Owner  string  `json:"owner"`
	Name   string  `json:"name"`
	PID    int     `json:"pid"`
	Type   AppType `json:"-"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	X      int     `json:"x"`
	Y      int     `json:"y"`
}

// Label returns a display string for the selector.
func (w WindowInfo) Label() string {
	name := w.Name
	if name == "" {
		name = "(untitled)"
	}
	// Truncate long names
	if len(name) > 60 {
		name = name[:57] + "..."
	}
	return fmt.Sprintf("%s — %s", strings.ToLower(w.Owner), name)
}

// swiftScript uses Swift to call CGWindowListCopyWindowInfo for CGWindowIDs.
// Swift has direct CoreGraphics access unlike JXA which has bridging issues.
const swiftScript = `
import CoreGraphics
import Foundation

let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
guard let windowList = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else {
    print("[]")
    exit(0)
}

var result: [[String: Any]] = []
let skip = Set(["Dock", "SystemUIServer", "Control Center", "WindowManager", "Notification Center", "Window Server"])

for w in windowList {
    let layer = w["kCGWindowLayer"] as? Int ?? 0
    guard layer == 0 else { continue }

    let owner = w["kCGWindowOwnerName"] as? String ?? ""
    guard !skip.contains(owner), !owner.isEmpty else { continue }

    let bounds = w["kCGWindowBounds"] as? [String: Any] ?? [:]
    let width = Int(bounds["Width"] as? Double ?? Double(bounds["Width"] as? Int ?? 0))
    let height = Int(bounds["Height"] as? Double ?? Double(bounds["Height"] as? Int ?? 0))
    guard width >= 100, height >= 100 else { continue }

    result.append([
        "id": w["kCGWindowNumber"] as? Int ?? 0,
        "owner": owner,
        "name": w["kCGWindowName"] as? String ?? "",
        "pid": w["kCGWindowOwnerPID"] as? Int ?? 0,
        "width": width,
        "height": height,
        "x": Int(bounds["X"] as? Double ?? 0),
        "y": Int(bounds["Y"] as? Double ?? 0)
    ])
}

let json = try! JSONSerialization.data(withJSONObject: result, options: [])
print(String(data: json, encoding: .utf8)!)
`

// jxaWindowNames uses System Events via JXA to get window titles keyed by PID.
// CGWindowListCopyWindowInfo requires Screen Recording permission for names,
// but System Events can get them with just Accessibility permission.
const jxaWindowNames = `
var se = Application("System Events");
var procs = se.processes.whose({visible: true, backgroundOnly: false})();
var result = {};
for (var i = 0; i < procs.length; i++) {
  var proc = procs[i];
  try {
    var pid = proc.unixId();
    var wins = proc.windows();
    for (var j = 0; j < wins.length; j++) {
      var name = wins[j].name() || "";
      if (name) {
        var key = pid + ":" + j;
        result[key] = name;
      }
    }
  } catch(e) {}
}
JSON.stringify(result);
`

// listWindows enumerates all visible application windows on macOS.
// Uses Swift for CGWindowIDs (needed for screencapture -l) and
// System Events for window names (doesn't require Screen Recording permission).
func listWindows() ([]WindowInfo, error) {
	// Step 1: Get window IDs via Swift/CoreGraphics
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "swift", "-e", swiftScript)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("window detection timed out (10s)")
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("window detection failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("window detection failed: %w", err)
	}

	var windows []WindowInfo
	if err := json.Unmarshal(out, &windows); err != nil {
		return nil, fmt.Errorf("parsing window list: %w", err)
	}

	// Step 2: Enrich with window names from System Events (keyed by PID)
	names := getWindowNames()
	for i := range windows {
		windows[i].Type = classifyApp(windows[i].Owner)

		// If CGWindowList didn't provide a name, try System Events
		if windows[i].Name == "" {
			pid := fmt.Sprintf("%d", windows[i].PID)
			if n, ok := names[pid]; ok {
				windows[i].Name = n
			}
		}
	}

	return windows, nil
}

// getWindowNames fetches window titles via System Events AppleScript.
// Returns a map of PID (as string) → first window title.
func getWindowNames() map[string]string {
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", jxaWindowNames)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Parse {"PID:0": "title", ...} and collapse to {"PID": "title"}
	var raw map[string]string
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil
	}

	result := make(map[string]string)
	for key, val := range raw {
		parts := strings.SplitN(key, ":", 2)
		pid := parts[0]
		if _, exists := result[pid]; !exists {
			result[pid] = val
		}
	}
	return result
}

// classifyApp maps a process name to an AppType.
func classifyApp(owner string) AppType {
	lower := strings.ToLower(owner)

	// Terminals
	terminals := []string{
		"terminal", "iterm", "ghostty", "warp", "kitty",
		"alacritty", "hyper", "wezterm", "rio", "tabby", "cmux",
	}
	for _, t := range terminals {
		if strings.Contains(lower, t) {
			return AppTerminal
		}
	}

	// Browsers
	browsers := []string{
		"safari", "chrome", "firefox", "opera", "arc",
		"brave", "edge", "vivaldi", "zen", "orion",
	}
	for _, b := range browsers {
		if strings.Contains(lower, b) {
			return AppBrowser
		}
	}

	return AppGeneric
}
