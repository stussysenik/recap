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
const swiftScript = `
import CoreGraphics
import Foundation

// .optionAll finds windows on all Spaces, minimized, and fullscreen
let allOptions: CGWindowListOption = [.optionAll, .excludeDesktopElements]
guard let windowList = CGWindowListCopyWindowInfo(allOptions, kCGNullWindowID) as? [[String: Any]] else {
    print("[]")
    exit(0)
}

// Also query on-screen-only to know which windows are currently visible
let onScreenOptions: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
let onScreenList = CGWindowListCopyWindowInfo(onScreenOptions, kCGNullWindowID) as? [[String: Any]] ?? []
var onScreenIDs = Set<Int>()
for w in onScreenList {
    if let wid = w["kCGWindowNumber"] as? Int { onScreenIDs.insert(wid) }
}

var result: [[String: Any]] = []
let skip = Set(["dock", "systemuiserver", "control center", "windowmanager", "notification center", "window server",
                 "autofill", "loginwindow", "open and save panel service", "universalaccessauthwarn",
                 "bzbmenu", "simulator"])

for w in windowList {
    let layer = w["kCGWindowLayer"] as? Int ?? 0
    guard layer == 0 else { continue }

    let owner = w["kCGWindowOwnerName"] as? String ?? ""
    guard !skip.contains(owner.lowercased()), !owner.isEmpty else { continue }

    // Filter out invisible/daemon windows (alpha == 0)
    let alpha = w["kCGWindowAlpha"] as? Double ?? 1.0
    guard alpha > 0 else { continue }

    // Filter out windows that are not normal (e.g. utility panels)
    let storeType = w["kCGWindowStoreType"] as? Int ?? 1
    guard storeType != 0 else { continue }

    let bounds = w["kCGWindowBounds"] as? [String: Any] ?? [:]
    let width = Int(bounds["Width"] as? Double ?? Double(bounds["Width"] as? Int ?? 0))
    let height = Int(bounds["Height"] as? Double ?? Double(bounds["Height"] as? Int ?? 0))
    guard width >= 100, height >= 100 else { continue }

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
        "onscreen": onScreenIDs.contains(windowID)
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

// getActivePID returns the PID of the frontmost (active) application.
// Returns -1 if no frontmost app could be determined.
func getActivePID() int {
	script := `import Cocoa
let workspace = NSWorkspace.shared
if let app = workspace.frontmostApplication {
    print(app.processIdentifier)
} else {
    print(-1)
}`
	cmd := exec.Command("swift", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.Output()
	if err != nil {
		return -1
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &pid)
	return pid
}

// getFrontmostAppName returns the name of the frontmost (active) application.
// Returns empty string if no frontmost app could be determined.
func getFrontmostAppName() string {
	script := `import Cocoa
let workspace = NSWorkspace.shared
if let app = workspace.frontmostApplication {
    print(app.localizedName ?? "")
} else {
    print("")
}`
	cmd := exec.Command("swift", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

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

	// Step 2: Get frontmost app for active window detection
	activePID := getActivePID()
	activeAppName := getFrontmostAppName()

	// Step 3: Enrich with window names from System Events (keyed by PID)
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

		// Mark frontmost window as active (match by PID or app name)
		if activePID > 0 && windows[i].PID == activePID {
			windows[i].IsActive = true
		} else if activeAppName != "" && strings.EqualFold(windows[i].Owner, activeAppName) {
			windows[i].IsActive = true
		}
	}

	// Step 4: Deduplicate — same PID → keep largest window
	windows = deduplicateWindows(windows)

	// Step 5: Sort — on-screen first, then terminals first
	sortWindows(windows)

	return windows, nil
}

// deduplicateWindows keeps the largest window per PID to avoid showing
// multiple entries for the same application (e.g. off-screen duplicates).
func deduplicateWindows(windows []WindowInfo) []WindowInfo {
	best := make(map[int]int) // PID → index of largest window
	for i, w := range windows {
		area := w.Width * w.Height
		if existing, ok := best[w.PID]; ok {
			existingArea := windows[existing].Width * windows[existing].Height
			if area > existingArea {
				best[w.PID] = i
			}
		} else {
			best[w.PID] = i
		}
	}

	var result []WindowInfo
	seen := make(map[int]bool)
	for _, w := range windows {
		idx := best[w.PID]
		if seen[w.PID] {
			continue
		}
		seen[w.PID] = true
		result = append(result, windows[idx])
	}
	return result
}

// sortWindows orders windows: on-screen first, then terminals before
// browsers before generic apps.
func sortWindows(windows []WindowInfo) {
	sort.SliceStable(windows, func(i, j int) bool {
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
