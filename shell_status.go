package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// cmdShellStatus shows the current state of shell session tracking.
// Displays all active sessions, validates PIDs, and cleans up stale entries.
func cmdShellStatus() {
	// Clean up stale entries first
	cleanupStaleSessions()

	sessions := listActiveSessions()

	if hasFlag("--json") {
		data, _ := json.MarshalIndent(sessions, "", "  ")
		fmt.Println(string(data))
		return
	}

	if len(sessions) == 0 {
		fmt.Println("No active shell sessions tracked.")
		fmt.Println()
		fmt.Println("To enable shell session tracking, add to your shell config:")
		fmt.Println("  zsh:  eval \"$(recap shell-init zsh)\"")
		fmt.Println("  bash: eval \"$(recap shell-init bash)\"")
		fmt.Println("  fish: recap shell-init fish | source")
		return
	}

	fmt.Printf("\033[1mActive Shell Sessions:\033[0m (%d tracked)\n\n", len(sessions))

	for _, s := range sessions {
		shell := s.Shell
		if idx := strings.LastIndex(shell, "/"); idx >= 0 {
			shell = shell[idx+1:]
		}

		cwd := s.CWD
		if home, err := os.UserHomeDir(); err == nil {
			cwd = strings.Replace(cwd, home, "~", 1)
		}

		age := time.Since(s.UpdatedAt).Round(time.Second)
		ageStr := age.String()
		if age < time.Minute {
			ageStr = "\033[32m" + ageStr + "\033[0m" // green = fresh
		} else if age < 5*time.Minute {
			ageStr = "\033[33m" + ageStr + "\033[0m" // yellow = aging
		} else {
			ageStr = "\033[90m" + ageStr + "\033[0m" // gray = stale
		}

		fmt.Printf("  \033[1m%s\033[0m (PID %d, %s)\n", shell, s.PID, s.TTY)
		fmt.Printf("    CWD: %s\n", cwd)
		if s.LastCmd != "" {
			fmt.Printf("    Last: %s\n", s.LastCmd)
		}
		fmt.Printf("    Updated: %s ago\n", ageStr)
		fmt.Println()
	}
}
