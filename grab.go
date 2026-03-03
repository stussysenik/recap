package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func cmdGrab() {
	format := "pdf"
	if hasFlag("--png") {
		format = "png"
	}
	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}
	wantEdit := hasFlag("--edit") || hasFlag("-e")
	source := getFlag("--from")

	var data []byte
	var err error
	var sourceLabel string

	switch source {
	case "tmux":
		data, err = grabTmux()
		sourceLabel = "tmux scrollback"
	case "clipboard", "pb":
		data, err = grabClipboard()
		sourceLabel = "clipboard"
	case "":
		// Auto-detect: try tmux first, then clipboard
		if inTmux() {
			data, err = grabTmux()
			sourceLabel = "tmux scrollback"
		} else {
			data, err = grabClipboard()
			sourceLabel = "clipboard"
		}
	default:
		// Treat as file path
		data, err = os.ReadFile(source)
		sourceLabel = source
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	if len(data) == 0 {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no content captured from %s\n", sourceLabel)
		fmt.Fprintf(os.Stderr, "\nUsage:\n")
		fmt.Fprintf(os.Stderr, "  recap grab                  %s# auto-detect (tmux > clipboard)%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "  recap grab --from=tmux      %s# capture tmux scrollback%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "  recap grab --from=clipboard %s# capture from clipboard%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "  recap grab --from=file.log  %s# capture from file%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "  recap grab --edit           %s# open in nvim first%s\n", "\033[90m", "\033[0m")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Captured %d bytes from %s\n", len(data), sourceLabel)

	// Optional editor pass
	if wantEdit {
		data, err = editInEditor(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m editor: %v\n", err)
			os.Exit(1)
		}
	}

	sess := &Session{
		ID:    time.Now().Format("2006-01-02_15-04-05"),
		Start: time.Now(),
		End:   time.Now(),
		CWD:   sourceLabel,
		Shell: "grab",
		Cols:  120,
		Rows:  50,
	}

	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputPath = filepath.Join(home, "Desktop",
			fmt.Sprintf("recap-%s.%s", sess.ID, format))
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Rendering %s...\n", format)

	if format == "png" {
		err = RenderSessionPNG(sess, outputPath, data)
	} else {
		err = RenderSessionPDF(sess, outputPath, data)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", outputPath)
	openFile(outputPath)
}

func grabTmux() ([]byte, error) {
	// -p: output to stdout, -S -: full scrollback, -e: include escape sequences (ANSI)
	cmd := exec.Command("tmux", "capture-pane", "-p", "-S", "-", "-e")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tmux capture-pane failed: %w", err)
	}
	return out, nil
}

func grabClipboard() ([]byte, error) {
	cmd := exec.Command("pbpaste")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("pbpaste failed: %w", err)
	}
	return out, nil
}

func inTmux() bool {
	return os.Getenv("TMUX") != ""
}

// grabScreen captures the frontmost terminal window as a screenshot.
// Returns the path to the captured image.
func grabScreen(outputPath string) error {
	// Use screencapture in interactive window mode
	// -o: no shadow, -x: no sound, -w: window mode
	// The user clicks the window to capture
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Click the terminal window to capture...\n")
	cmd := exec.Command("screencapture", "-o", "-x", "-w", outputPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdScreen() {
	home, _ := os.UserHomeDir()
	ts := time.Now().Format("2006-01-02_15-04-05")

	if hasFlag("--pages") {
		// Multi-page capture mode
		cmdScreenPages()
		return
	}

	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}
	if outputPath == "" {
		outputPath = filepath.Join(home, "Desktop", fmt.Sprintf("recap-screen-%s.png", ts))
	}

	if err := grabScreen(outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	// Check if file was created (user might have cancelled)
	if _, err := os.Stat(outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Capture cancelled\n")
		return
	}

	fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", outputPath)
	openFile(outputPath)
}

func cmdScreenPages() {
	home, _ := os.UserHomeDir()
	ts := time.Now().Format("2006-01-02_15-04-05")
	tmpDir, err := os.MkdirTemp("", "recap-pages-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Fprintf(os.Stderr, "\033[1m[recap] Multi-page capture mode\033[0m\n")
	fmt.Fprintf(os.Stderr, "  Scroll your terminal to the start of what you want to capture.\n")
	fmt.Fprintf(os.Stderr, "  Press \033[1mEnter\033[0m to capture each page, \033[1mq\033[0m to finish.\n\n")

	var pages []string
	pageNum := 1

	for {
		fmt.Fprintf(os.Stderr, "\033[90m[page %d]\033[0m Press Enter to capture (q to finish): ", pageNum)
		var input string
		fmt.Scanln(&input)

		if strings.TrimSpace(strings.ToLower(input)) == "q" {
			break
		}

		pagePath := filepath.Join(tmpDir, fmt.Sprintf("page-%03d.png", pageNum))
		if err := grabScreen(pagePath); err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m capture failed: %v\n", err)
			continue
		}

		if _, err := os.Stat(pagePath); err != nil {
			fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m Capture cancelled, try again\n")
			continue
		}

		pages = append(pages, pagePath)
		fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m Page %d captured\n", pageNum)
		pageNum++

		fmt.Fprintf(os.Stderr, "  Now scroll down in your terminal, then press Enter for next page.\n")
	}

	if len(pages) == 0 {
		fmt.Fprintf(os.Stderr, "\033[33m[recap]\033[0m No pages captured\n")
		return
	}

	// For now, just output individual pages. Image stitching can be added later.
	outputDir := filepath.Join(home, "Desktop", fmt.Sprintf("recap-pages-%s", ts))
	os.MkdirAll(outputDir, 0755)

	for i, p := range pages {
		dst := filepath.Join(outputDir, fmt.Sprintf("page-%03d.png", i+1))
		data, _ := os.ReadFile(p)
		os.WriteFile(dst, data, 0644)
	}

	fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %d pages saved to %s\n", len(pages), outputDir)
	openFile(outputDir)
}
