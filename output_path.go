package main

import (
	"os"
	"path/filepath"
)

func defaultOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return filepath.Join(home, "Downloads")
}

func defaultOutputPath(filename string) string {
	return filepath.Join(defaultOutputDir(), filename)
}
