package main

import (
	"os"
	"strings"
	"testing"
)

func TestSelectDetectItemsByFlagsWindowID(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"recap", "detect", "--window-id", "42"}
	items := []DetectItem{
		{Window: &WindowInfo{ID: 42, Owner: "Zed", Name: "settings.json"}},
		{Window: &WindowInfo{ID: 84, Owner: "Ghostty", Name: "project"}},
	}

	selected, err := selectDetectItemsByFlags(items)
	if err != nil {
		t.Fatalf("selectDetectItemsByFlags returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].Window == nil || selected[0].Window.ID != 42 {
		t.Fatalf("selected = %#v, want window 42", selected)
	}
}

func TestSelectDetectItemsByFlagsAppAndTitle(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"recap", "detect", "--app", "zed", "--title", "settings"}
	items := []DetectItem{
		{Window: &WindowInfo{ID: 42, Owner: "Zed", Name: "settings.json"}},
		{Window: &WindowInfo{ID: 84, Owner: "Ghostty", Name: "settings"}},
	}

	selected, err := selectDetectItemsByFlags(items)
	if err != nil {
		t.Fatalf("selectDetectItemsByFlags returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].Window == nil || selected[0].Window.ID != 42 {
		t.Fatalf("selected = %#v, want window 42", selected)
	}
}

func TestSelectDetectItemsByFlagsActiveWindow(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"recap", "detect", "--active-window"}
	items := []DetectItem{
		{Window: &WindowInfo{ID: 1, Owner: "Ghostty", Name: "project"}},
		{Window: &WindowInfo{ID: 2, Owner: "Zed", Name: "settings.json", IsActive: true}},
		{Tmux: &TmuxPane{SessionName: "main", WindowIndex: 0, PaneIndex: 0, Active: true}},
	}

	selected, err := selectDetectItemsByFlags(items)
	if err != nil {
		t.Fatalf("selectDetectItemsByFlags returned error: %v", err)
	}
	if len(selected) != 1 || selected[0].Window == nil || selected[0].Window.ID != 2 {
		t.Fatalf("selected = %#v, want active window 2", selected)
	}
}

func TestSelectDetectItemsByFlagsAmbiguousTitle(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{"recap", "detect", "--app", "zed"}
	items := []DetectItem{
		{Window: &WindowInfo{ID: 1, Owner: "Zed", Name: "a"}},
		{Window: &WindowInfo{ID: 2, Owner: "Zed", Name: "b"}},
	}

	_, err := selectDetectItemsByFlags(items)
	if err == nil {
		t.Fatalf("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "multiple windows matched") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "[1]") || !strings.Contains(err.Error(), "[2]") {
		t.Fatalf("expected window IDs in error, got: %v", err)
	}
}
