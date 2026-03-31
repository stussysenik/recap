package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ShellSession describes an active shell session registered via the shell hook.
// Written by the precmd/preexec hooks installed by `recap shell-init`.
type ShellSession struct {
	PID       int       `json:"pid"`
	TTY       string    `json:"tty"`
	Shell     string    `json:"shell"`
	CWD       string    `json:"cwd"`
	LastCmd   string    `json:"last_cmd,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Label returns a display string for the selector TUI.
func (s ShellSession) Label() string {
	cwd := s.CWD
	if home, err := os.UserHomeDir(); err == nil {
		cwd = strings.Replace(cwd, home, "~", 1)
	}
	shell := s.Shell
	if idx := strings.LastIndex(shell, "/"); idx >= 0 {
		shell = shell[idx+1:]
	}
	label := fmt.Sprintf("%s:%d — %s", shell, s.PID, cwd)
	if s.LastCmd != "" {
		label += fmt.Sprintf(" (%s)", s.LastCmd)
	}
	return label
}

// activeSessionsDir returns the path to the shell sessions registry directory.
func activeSessionsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".recap", "active")
}

// sessionFilePath returns the path for a specific PID's session file.
func sessionFilePath(pid int) string {
	return filepath.Join(activeSessionsDir(), fmt.Sprintf("%d.json", pid))
}

// writeShellSession atomically writes a shell session file.
// Uses write-to-temp + rename for atomicity.
func writeShellSession(s ShellSession) error {
	dir := activeSessionsDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	// Atomic write: temp file + rename
	tmpPath := sessionFilePath(s.PID) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, sessionFilePath(s.PID))
}

// listActiveSessions reads all session files from the registry,
// validates that each PID is still alive, and removes stale entries.
func listActiveSessions() []ShellSession {
	dir := activeSessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var sessions []ShellSession
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var s ShellSession
		if err := json.Unmarshal(data, &s); err != nil {
			os.Remove(path)
			continue
		}

		// Validate PID is still alive
		if !isPIDAlive(s.PID) {
			os.Remove(path)
			continue
		}

		// Remove sessions older than 24 hours (stale)
		if time.Since(s.UpdatedAt) > 24*time.Hour {
			os.Remove(path)
			continue
		}

		sessions = append(sessions, s)
	}

	return sessions
}

// isPIDAlive checks if a process is still running by sending signal 0.
func isPIDAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// cleanupStaleSessions removes session files for dead processes.
func cleanupStaleSessions() {
	dir := activeSessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}

		pidStr := strings.TrimSuffix(name, ".json")
		pid, err := strconv.Atoi(pidStr)
		if err != nil {
			os.Remove(filepath.Join(dir, name))
			continue
		}

		if !isPIDAlive(pid) {
			os.Remove(filepath.Join(dir, name))
		}
	}
}

// mergeShellSessions enriches libproc-discovered shells with CWD and command
// data from the shell hook registry. Returns the matched ShellSession for a
// given PID, or nil if no match.
func findShellSessionForPID(pid int, sessions []ShellSession) *ShellSession {
	for i := range sessions {
		if sessions[i].PID == pid {
			return &sessions[i]
		}
	}
	return nil
}
