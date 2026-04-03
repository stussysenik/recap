//go:build darwin

package main

import "testing"

func TestCorrelateShellsToWindowsWithParentsWalksThroughLogin(t *testing.T) {
	shells := []ShellProc{
		{PID: 101, PPID: 91, TTY: "ttys010", Comm: "zsh"},
	}
	windows := []WindowInfo{
		{PID: 10, Owner: "Ghostty"},
	}
	parentMap := map[int]int{
		91: 10, // login -> Ghostty
	}

	got := correlateShellsToWindowsWithParents(shells, windows, parentMap)
	matched := got[10]
	if len(matched) != 1 {
		t.Fatalf("matched shell count = %d, want 1", len(matched))
	}
	if matched[0].PID != 101 {
		t.Fatalf("matched shell PID = %d, want 101", matched[0].PID)
	}
}

func TestCorrelateShellsToWindowsWithParentsLeavesUnmatchedShellsOut(t *testing.T) {
	shells := []ShellProc{
		{PID: 202, PPID: 192, TTY: "ttys011", Comm: "zsh"},
	}
	windows := []WindowInfo{
		{PID: 10, Owner: "Ghostty"},
	}
	parentMap := map[int]int{
		192: 150,
		150: 1,
	}

	got := correlateShellsToWindowsWithParents(shells, windows, parentMap)
	if len(got) != 0 {
		t.Fatalf("unexpected matches: %#v", got)
	}
}
