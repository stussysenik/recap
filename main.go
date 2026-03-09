package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

const version = "0.3.0"

func main() {
	if len(os.Args) < 2 {
		cmdRecord()
		return
	}

	switch os.Args[1] {
	case "record", "rec":
		cmdRecord()
	case "claude", "c":
		cmdClaude()
	case "pipe", "p":
		cmdPipe()
	case "grab", "g":
		cmdGrab()
	case "detect", "d":
		cmdDetect()
	case "chat":
		cmdChat()
	case "screen":
		cmdScreen()
	case "snap", "export", "s":
		cmdSnap()
	case "list", "ls":
		cmdList()
	case "version", "--version", "-v":
		fmt.Printf("recap %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\nRun 'recap help' for usage.\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdSnap() {
	sessionID := getFlag("--session")
	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}
	format := "pdf"
	if hasFlag("--png") {
		format = "png"
	}

	var sess *Session
	var err error

	if sessionID != "" {
		sess, err = LoadSession(sessionID)
	} else {
		sess, err = LatestSession()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	if outputPath == "" {
		ext := format
		home, _ := os.UserHomeDir()
		outputPath = fmt.Sprintf("%s/Desktop/recap-%s.%s", home, sess.ID, ext)
	}

	fmt.Printf("\033[90m[recap]\033[0m Exporting session \033[1m%s\033[0m...\n", sess.ID)

	data, err := os.ReadFile(sess.OutputPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m reading session: %v\n", err)
		os.Exit(1)
	}

	if format == "png" {
		err = RenderSessionPNG(sess, outputPath, data)
	} else {
		err = RenderSessionPDF(sess, outputPath, data)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\033[32m✓\033[0m Exported to %s\n", outputPath)
	openFile(outputPath)
}

func cmdList() {
	sessions, err := ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions recorded yet.")
		fmt.Println("Run \033[1mrecap\033[0m to start recording.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\033[1mID\tDURATION\tDIRECTORY\033[0m")

	for _, s := range sessions {
		duration := "\033[33mactive\033[0m"
		if !s.End.IsZero() {
			duration = s.End.Sub(s.Start).Round(time.Second).String()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.ID, duration, s.CWD)
	}
	w.Flush()
}

func printUsage() {
	fmt.Print(`
  ┌──────────────────────────────────────────────────┐
  │  ◉ recap — terminal output as beautiful PDFs     │
  └──────────────────────────────────────────────────┘

  Capture:
    recap                 Record a shell session (PTY wrapper)
    recap detect          Detect windows → select → capture → PDF
    recap detect --list   List detected windows with details
    recap chat            Quick Ghostty capture (all panes, no TUI)
    recap grab            Capture scrollback (tmux/clipboard/file)
    recap grab --edit     Capture → open in nvim → trim → render
    recap pipe            Read from stdin, render as PDF
    recap screen          Screenshot terminal window
    recap screen --pages  Multi-page screenshot → stitched PDF

  Export:
    recap snap            Export last recorded session
    recap list            List recorded sessions

  Pipe examples:
    pbpaste | recap pipe              # clipboard → PDF
    cat session.log | recap pipe      # file → PDF
    tmux capture-pane -pS- -e | recap pipe   # tmux → PDF
    command 2>&1 | recap pipe --png   # command → PNG

  Grab sources (auto-detects):
    --from=tmux           tmux scrollback with ANSI
    --from=clipboard      macOS clipboard (pbpaste)
    --from=<file>         read from file

  Global flags:
    --png                 Output PNG instead of PDF
    --output=PATH         Custom output path
    --edit, -e            Open in $EDITOR before rendering
    --title=TEXT          Custom header title

  In-session shortcuts (during recap record):
    Ctrl+] then s         Snap → PDF
    Ctrl+] then p         Snap → PNG
    Ctrl+] then q         Quit recording
    Ctrl+] Ctrl+]         Send literal Ctrl+]

  Ghostty:
    Split panes are detected automatically via Accessibility API.
    Each pane is captured as a separate PDF.
    Requires: System Settings → Privacy & Security → Accessibility

  Permissions:
    Screen Recording    Required for window detection and capture
    Accessibility       Required for Ghostty split pane detection

  Version: ` + version + `

`)
}

// Helpers for simple flag parsing
func getFlag(prefix string) string {
	args := os.Args[2:]
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix+"=") {
			return arg[len(prefix)+1:]
		}
		if arg == prefix && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(name string) bool {
	for _, arg := range os.Args[2:] {
		if arg == name {
			return true
		}
	}
	return false
}
