package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func cmdPipe() {
	format := "pdf"
	if hasFlag("--png") {
		format = "png"
	}
	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}
	wantEdit := hasFlag("--edit") || hasFlag("-e")
	title := getFlag("--title")
	if title == "" {
		title = "terminal session"
	}

	// Read all stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m reading stdin: %v\n", err)
		os.Exit(1)
	}

	if len(data) == 0 {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m no input received on stdin\n")
		fmt.Fprintf(os.Stderr, "\nUsage:\n")
		fmt.Fprintf(os.Stderr, "  pbpaste | recap pipe              %s# from clipboard%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "  cat session.log | recap pipe      %s# from file%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "  tmux capture-pane -p -S - -e | recap pipe  %s# from tmux%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "  some-command 2>&1 | recap pipe    %s# from command%s\n", "\033[90m", "\033[0m")
		fmt.Fprintf(os.Stderr, "\nFlags:\n")
		fmt.Fprintf(os.Stderr, "  --edit, -e     Open in $EDITOR first (nvim motions to select/trim)\n")
		fmt.Fprintf(os.Stderr, "  --png          Output as PNG instead of PDF\n")
		fmt.Fprintf(os.Stderr, "  --output=PATH  Custom output path\n")
		fmt.Fprintf(os.Stderr, "  --title=TEXT   Custom title for the header\n")
		os.Exit(1)
	}

	// Optionally open in editor for trimming/selection
	if wantEdit {
		edited, err := editInEditor(data)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m editor: %v\n", err)
			os.Exit(1)
		}
		data = edited
	}

	// Transient session (not persisted to ~/.recap)
	sess := &Session{
		ID:    time.Now().Format("2006-01-02_15-04-05"),
		Start: time.Now(),
		End:   time.Now(),
		CWD:   title,
		Shell: "pipe",
		Cols:  120,
		Rows:  50,
	}

	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputPath = filepath.Join(home, "Desktop",
			fmt.Sprintf("recap-%s.%s", sess.ID, format))
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m %d bytes → %s\n", len(data), format)

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

// editInEditor opens data in $EDITOR, returns the edited content.
// User can use nvim motions to scroll, select, trim content.
func editInEditor(data []byte) ([]byte, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp("", "recap-edit-*.txt")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return nil, err
	}
	tmpFile.Close()

	// Open editor
	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Opening in %s — edit/trim, then :wq to render\n", editor)

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor exited with error: %w", err)
	}

	// Read back edited content
	return os.ReadFile(tmpPath)
}
