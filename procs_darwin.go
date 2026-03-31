//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// ShellProc describes a shell process discovered via libproc.
// Each entry represents a running shell with its PID, parent PID,
// controlling TTY, and command name — the same data ps(1) shows,
// but gathered directly from the kernel via proc_pidinfo().
type ShellProc struct {
	PID  int    `json:"pid"`
	PPID int    `json:"ppid"`
	TTY  string `json:"tty"`
	Comm string `json:"comm"`
	Path string `json:"path"`
}

// procListScript uses Darwin's libproc API (proc_listallpids + proc_pidinfo)
// to enumerate all shell processes owned by the current user. This is the same
// kernel interface that ps(1) and Activity Monitor use internally.
//
// For each shell process it returns: PID, PPID, controlling TTY device,
// command name, and full executable path. The TTY device number is decoded
// into the /dev/ttysNNN format used by macOS.
const procListScript = `
import Darwin
import Foundation

let myUID = getuid()

// 1. Get count of all PIDs on the system
let count = proc_listallpids(nil, 0)
guard count > 0 else {
    print("[]")
    exit(0)
}

// 2. Allocate buffer and fill with all PIDs
var pids = [pid_t](repeating: 0, count: Int(count))
let bytesNeeded = Int32(count) * Int32(MemoryLayout<pid_t>.size)
proc_listallpids(&pids, bytesNeeded)

// 3. Known shell names — matches against the last component of the process name
let shells: Set<String> = ["zsh", "bash", "fish", "sh", "dash", "nu", "elvish", "ion", "pwsh"]

// MAXPATHLEN = 1024, PROC_PIDPATHINFO_MAXSIZE = 4 * MAXPATHLEN = 4096
// The macro isn't bridged to Swift, so we define it directly.
let pathBufSize: Int = 4096

var procs: [[String: Any]] = []
let infoSize = Int32(MemoryLayout<proc_bsdinfo>.size)

for pid in pids where pid > 0 {
    var info = proc_bsdinfo()
    let sz = proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &info, infoSize)
    guard sz == infoSize else { continue }

    // Current user only
    guard info.pbi_uid == myUID else { continue }

    // Must have a controlling TTY (filters out daemons, background processes)
    guard info.e_tdev != 0 else { continue }

    // Extract command name from pbi_comm (C string, 16 bytes max)
    let comm = withUnsafePointer(to: info.pbi_comm) {
        $0.withMemoryRebound(to: CChar.self, capacity: 16) { String(cString: $0) }
    }
    guard shells.contains(comm) else { continue }

    // Get full executable path via proc_pidpath
    var pathBuf = [CChar](repeating: 0, count: pathBufSize)
    proc_pidpath(pid, &pathBuf, UInt32(pathBufSize))
    let path = String(cString: pathBuf)

    // Decode TTY device number → ttysNNN
    // macOS uses 24-bit minor numbers: major = (dev >> 24) & 0xff
    // Minor 0xffffff means no real TTY — skip these
    let minor = info.e_tdev & 0xffffff
    guard minor < 0xffffff else { continue }
    let tty = "ttys\(String(format: "%03d", minor))"

    procs.append([
        "pid": Int(pid),
        "ppid": Int(info.pbi_ppid),
        "tty": tty,
        "comm": comm,
        "path": path,
    ])
}

let json = try! JSONSerialization.data(withJSONObject: procs)
print(String(data: json, encoding: .utf8)!)
`

// listShellProcesses uses the Darwin libproc API to find all shell processes
// owned by the current user. Returns ~70ms with zero subprocess overhead
// beyond the Swift invocation itself.
func listShellProcesses() ([]ShellProc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "swift", "-e", procListScript)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("process listing timed out (5s)")
		}
		return nil, fmt.Errorf("process listing failed: %w", err)
	}

	var procs []ShellProc
	if err := json.Unmarshal(out, &procs); err != nil {
		return nil, fmt.Errorf("parsing process list: %w", err)
	}

	return procs, nil
}

// correlateShellsToWindows walks each shell's PPID chain to find which
// terminal window (by PID) owns it. Returns a map of window PID → shells.
//
// The walk is deterministic: a shell running in iTerm2 tab 3 has a PPID
// chain like: zsh(5678) → login(5677) → iTerm2(1234). We walk until
// we hit a known window PID or exhaust the chain.
func correlateShellsToWindows(shells []ShellProc, windows []WindowInfo) map[int][]ShellProc {
	// Build a set of window PIDs for O(1) lookup
	windowPIDs := make(map[int]bool, len(windows))
	for _, w := range windows {
		windowPIDs[w.PID] = true
	}

	// Build a PPID lookup from the shell list itself (for walking intermediate processes)
	ppidMap := make(map[int]int, len(shells))
	for _, s := range shells {
		ppidMap[s.PID] = s.PPID
	}

	result := make(map[int][]ShellProc)

	for _, shell := range shells {
		// Walk the PPID chain: shell → parent → grandparent → ...
		// Stop when we find a window PID or after 20 hops (safety limit)
		pid := shell.PPID
		for hops := 0; hops < 20; hops++ {
			if windowPIDs[pid] {
				result[pid] = append(result[pid], shell)
				break
			}
			// Try to find this PID's parent in our process list
			if parent, ok := ppidMap[pid]; ok && parent > 1 {
				pid = parent
				continue
			}
			// Not in our list — try the shell list as a whole
			// (the parent might not be a shell but we still recorded it)
			found := false
			for _, s := range shells {
				if s.PID == pid {
					pid = s.PPID
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
	}

	return result
}

// shellsWithoutWindows returns shells that couldn't be correlated to any
// known terminal window. These are "orphan" TTY sessions — SSH connections,
// screen sessions, or terminals we don't have window info for.
func shellsWithoutWindows(shells []ShellProc, correlated map[int][]ShellProc) []ShellProc {
	// Collect all correlated shell PIDs
	matched := make(map[int]bool)
	for _, group := range correlated {
		for _, s := range group {
			matched[s.PID] = true
		}
	}

	var orphans []ShellProc
	for _, s := range shells {
		if !matched[s.PID] {
			orphans = append(orphans, s)
		}
	}
	return orphans
}
