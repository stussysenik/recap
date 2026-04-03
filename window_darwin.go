//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
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
	ID       int     `json:"id"`
	Owner    string  `json:"owner"`
	Name     string  `json:"name"`
	PID      int     `json:"pid"`
	Type     AppType `json:"-"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	X        int     `json:"x"`
	Y        int     `json:"y"`
	OnScreen bool    `json:"onscreen"`
	IsActive bool    `json:"is_active"` // whether this is the frontmost window
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
	label := fmt.Sprintf("%s — %s", strings.ToLower(w.Owner), name)
	if w.IsActive {
		label += " \033[35m(this window)\033[0m" // magenta badge
	}
	return label
}

// swiftScript uses Swift to call CGWindowListCopyWindowInfo for CGWindowIDs.
// Uses .optionAll to discover windows across ALL Spaces, minimized, and fullscreen.
// Swift has direct CoreGraphics access unlike JXA which has bridging issues.
// We keep distinct windows even when they share a PID because multi-window
// apps such as Ghostty expose one app PID for many real windows.
const swiftScript = `
import CoreGraphics
import Foundation

let allOptions: CGWindowListOption = [.optionAll, .excludeDesktopElements]
let skip = Set(["dock", "systemuiserver", "control center", "windowmanager", "notification center", "window server",
                 "autofill", "loginwindow", "open and save panel service", "universalaccessauthwarn",
                 "bzbmenu", "simulator"])

func shouldInclude(_ w: [String: Any]) -> Bool {
    let layer = w["kCGWindowLayer"] as? Int ?? 0
    guard layer == 0 else { return false }

    let owner = w["kCGWindowOwnerName"] as? String ?? ""
    guard !skip.contains(owner.lowercased()), !owner.isEmpty else { return false }

    let alpha = w["kCGWindowAlpha"] as? Double ?? 1.0
    guard alpha > 0 else { return false }

    let storeType = w["kCGWindowStoreType"] as? Int ?? 1
    guard storeType != 0 else { return false }

    let bounds = w["kCGWindowBounds"] as? [String: Any] ?? [:]
    let width = Int(bounds["Width"] as? Double ?? Double(bounds["Width"] as? Int ?? 0))
    let height = Int(bounds["Height"] as? Double ?? Double(bounds["Height"] as? Int ?? 0))
    guard width >= 100, height >= 100 else { return false }

    return true
}

guard let windowList = CGWindowListCopyWindowInfo(allOptions, kCGNullWindowID) as? [[String: Any]] else {
    print("[]")
    exit(0)
}

// Also query on-screen-only to know which windows are currently visible and
// which concrete window is frontmost.
let onScreenOptions: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
let onScreenList = CGWindowListCopyWindowInfo(onScreenOptions, kCGNullWindowID) as? [[String: Any]] ?? []
var onScreenIDs = Set<Int>()
var frontWindowID = 0
for w in onScreenList {
    if let wid = w["kCGWindowNumber"] as? Int { onScreenIDs.insert(wid) }
    if frontWindowID == 0, shouldInclude(w), let wid = w["kCGWindowNumber"] as? Int {
        frontWindowID = wid
    }
}

var result: [[String: Any]] = []
for w in windowList {
    guard shouldInclude(w) else { continue }
    let owner = w["kCGWindowOwnerName"] as? String ?? ""

    let bounds = w["kCGWindowBounds"] as? [String: Any] ?? [:]
    let width = Int(bounds["Width"] as? Double ?? Double(bounds["Width"] as? Int ?? 0))
    let height = Int(bounds["Height"] as? Double ?? Double(bounds["Height"] as? Int ?? 0))
    let windowID = w["kCGWindowNumber"] as? Int ?? 0

    result.append([
        "id": windowID,
        "owner": owner,
        "name": w["kCGWindowName"] as? String ?? "",
        "pid": w["kCGWindowOwnerPID"] as? Int ?? 0,
        "width": width,
        "height": height,
        "x": Int(bounds["X"] as? Double ?? 0),
        "y": Int(bounds["Y"] as? Double ?? 0),
        "onscreen": onScreenIDs.contains(windowID),
        "is_active": windowID == frontWindowID
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
var procs = se.processes.whose({backgroundOnly: false})();
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

// listWindows enumerates ALL application windows on macOS, including windows
// on other Spaces, minimized, and fullscreen. Uses Swift for CGWindowIDs
// (needed for screencapture -l) and System Events for window names.
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

	// Step 3: Deduplicate only exact copies while preserving real multi-window apps.
	windows = deduplicateWindows(windows)

	// Step 4: Sort — active first, then on-screen, then terminals first.
	sortWindows(windows)

	return windows, nil
}

// deduplicateWindows removes exact duplicate entries while preserving real
// multi-window apps that share a PID.
func deduplicateWindows(windows []WindowInfo) []WindowInfo {
	type key struct {
		PID         int
		Owner, Name string
		X, Y, W, H  int
	}

	score := func(w WindowInfo) int {
		n := 0
		if w.Name != "" {
			n += 4
		}
		if w.OnScreen {
			n += 2
		}
		if w.IsActive {
			n++
		}
		return n
	}

	best := make(map[key]int)
	for i, w := range windows {
		k := key{
			PID:   w.PID,
			Owner: w.Owner,
			Name:  w.Name,
			X:     w.X,
			Y:     w.Y,
			W:     w.Width,
			H:     w.Height,
		}
		if existing, ok := best[k]; ok {
			if score(w) > score(windows[existing]) {
				best[k] = i
			}
		} else {
			best[k] = i
		}
	}

	var result []WindowInfo
	seen := make(map[key]bool)
	for _, w := range windows {
		k := key{
			PID:   w.PID,
			Owner: w.Owner,
			Name:  w.Name,
			X:     w.X,
			Y:     w.Y,
			W:     w.Width,
			H:     w.Height,
		}
		idx := best[k]
		if seen[k] {
			continue
		}
		seen[k] = true
		result = append(result, windows[idx])
	}
	return result
}

// sortWindows orders windows: on-screen first, then terminals before
// browsers before generic apps.
func sortWindows(windows []WindowInfo) {
	sort.SliceStable(windows, func(i, j int) bool {
		// Active window comes first.
		if windows[i].IsActive != windows[j].IsActive {
			return windows[i].IsActive
		}
		// On-screen windows come first
		if windows[i].OnScreen != windows[j].OnScreen {
			return windows[i].OnScreen
		}
		// Terminals first, then browsers, then generic
		return windows[i].Type < windows[j].Type
	})
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
