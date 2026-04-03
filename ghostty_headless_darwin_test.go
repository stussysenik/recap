//go:build darwin

package main

import "testing"

func TestParseGhosttySelectedTabTerminals(t *testing.T) {
	raw := "⠋ v0-ipod" + ghosttyRecordSep +
		"FD962379-D4BD-410E-8719-2E75CA3D4ED6" + ghosttyFieldSep +
		"⠇ v0-ipod" + ghosttyFieldSep +
		"/Users/s3nik/Desktop/v0-ipod" + ghosttyFieldSep +
		"1" + ghosttyRecordSep +
		"5FCD0B8E-91B5-43D9-9629-7806D6F85145" + ghosttyFieldSep +
		"✳ ipod-classic-anniversary-validation" + ghosttyFieldSep +
		"/Users/s3nik/Desktop/v0-ipod" + ghosttyFieldSep +
		"0" + ghosttyRecordSep

	terminals, windowTitle := parseGhosttySelectedTabTerminals(raw)
	if windowTitle != "⠋ v0-ipod" {
		t.Fatalf("window title = %q, want %q", windowTitle, "⠋ v0-ipod")
	}
	if len(terminals) != 2 {
		t.Fatalf("terminals len = %d, want 2", len(terminals))
	}
	if !terminals[0].Focused {
		t.Fatalf("first terminal should be focused")
	}
	if terminals[1].Focused {
		t.Fatalf("second terminal should not be focused")
	}
	if terminals[1].Name != "✳ ipod-classic-anniversary-validation" {
		t.Fatalf("second terminal name = %q", terminals[1].Name)
	}
}

func TestFindGhosttyTerminalMatches(t *testing.T) {
	terminals := []GhosttyTerminal{
		{Name: "⠇ v0-ipod", WorkingDirectory: "/Users/s3nik/Desktop/v0-ipod"},
		{Name: "✳ ipod-classic-anniversary-validation", WorkingDirectory: "/Users/s3nik/Desktop/v0-ipod"},
	}

	byTitle := findGhosttyTerminalMatches(terminals, "anniversary-validation")
	if len(byTitle) != 1 {
		t.Fatalf("title matches len = %d, want 1", len(byTitle))
	}
	if stripSpinner(byTitle[0].Name) != "ipod-classic-anniversary-validation" {
		t.Fatalf("matched title = %q", byTitle[0].Name)
	}

	byCWD := findGhosttyTerminalMatches(terminals, "desktop/v0-ipod")
	if len(byCWD) != 2 {
		t.Fatalf("cwd matches len = %d, want 2", len(byCWD))
	}
}
