package main

import (
	"os"
	"testing"
)

func TestGetFlagSupportsEqualsAndSeparateArgForms(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	os.Args = []string{
		"recap",
		"pipe",
		"--output=/tmp/out-a.png",
		"-o",
		"/tmp/out-b.png",
		"--title",
		"demo",
	}

	if got := getFlag("--output"); got != "/tmp/out-a.png" {
		t.Fatalf("getFlag(--output) = %q, want %q", got, "/tmp/out-a.png")
	}
	if got := getFlag("-o"); got != "/tmp/out-b.png" {
		t.Fatalf("getFlag(-o) = %q, want %q", got, "/tmp/out-b.png")
	}
	if got := getFlag("--title"); got != "demo" {
		t.Fatalf("getFlag(--title) = %q, want %q", got, "demo")
	}
}
