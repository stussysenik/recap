package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
)

const (
	prefixKey    byte          = 0x1D // Ctrl+]
	chordTimeout time.Duration = 2 * time.Second
)

type Recorder struct {
	session    *Session
	ptmx       *os.File
	outputFile *os.File
	mu         sync.Mutex
	outputBuf  []byte
	chordState int       // 0=normal, 1=prefix received
	chordTime  time.Time // when prefix key was pressed
}

func cmdRecord() {
	sess := NewSession()
	if err := sess.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m[recap] error:\033[0m %v\n", err)
		os.Exit(1)
	}

	shell := sess.Shell
	cmd := exec.Command(shell, "-l")
	cmd.Dir = sess.CWD
	cmd.Env = append(os.Environ(),
		"RECAP_SESSION="+sess.ID,
		"RECAP=1",
	)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m[recap] error:\033[0m failed to start PTY: %v\n", err)
		os.Exit(1)
	}
	defer ptmx.Close()

	// Handle terminal resize
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	sigCh <- syscall.SIGWINCH // initial size sync

	// Capture terminal dimensions
	if w, h, err := term.GetSize(int(os.Stdin.Fd())); err == nil {
		sess.Cols = w
		sess.Rows = h
	}

	// Print banner BEFORE raw mode
	fmt.Fprintf(os.Stderr,
		"\033[90m┌─────────────────────────────────────────────────┐\033[0m\n"+
			"\033[90m│\033[0m  \033[1m◉ recap\033[0m recording          \033[90mCtrl+] s\033[0m → snap  \033[90m│\033[0m\n"+
			"\033[90m│\033[0m  \033[90m%s\033[0m\033[90m│\033[0m\n"+
			"\033[90m└─────────────────────────────────────────────────┘\033[0m\n",
		padRight(sess.CWD, 41),
	)

	// Enter raw mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m[recap] error:\033[0m failed to set raw mode: %v\n", err)
		os.Exit(1)
	}

	// Open output file
	outputFile, err := os.Create(sess.OutputPath())
	if err != nil {
		term.Restore(int(os.Stdin.Fd()), oldState)
		fmt.Fprintf(os.Stderr, "\033[31m[recap] error:\033[0m %v\n", err)
		os.Exit(1)
	}

	rec := &Recorder{
		session:    sess,
		ptmx:       ptmx,
		outputFile: outputFile,
	}

	// Relay goroutines
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); rec.relayInput() }()
	go func() { defer wg.Done(); rec.relayOutput() }()

	// Wait for shell to exit
	_ = cmd.Wait()

	// Cleanup
	outputFile.Close()

	sess.End = time.Now()
	_ = sess.Save()

	// Restore terminal BEFORE printing farewell
	term.Restore(int(os.Stdin.Fd()), oldState)

	duration := sess.Duration().Round(time.Second)
	fmt.Fprintf(os.Stderr,
		"\n\033[90m[recap]\033[0m Session saved: \033[1m%s\033[0m (%s)\n",
		sess.ID, duration,
	)
}

func (r *Recorder) relayInput() {
	buf := make([]byte, 1024)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}

		for i := 0; i < n; i++ {
			b := buf[i]

			switch r.chordState {
			case 0: // Normal mode
				if b == prefixKey {
					r.chordState = 1
					r.chordTime = time.Now()
					continue
				}
				r.ptmx.Write([]byte{b})

			case 1: // After prefix key — waiting for chord
				r.chordState = 0

				if time.Since(r.chordTime) > chordTimeout {
					// Timed out — forward both bytes
					r.ptmx.Write([]byte{prefixKey, b})
					continue
				}

				switch b {
				case 's': // Snap → PDF
					go r.snapPDF()
				case 'p': // Snap → PNG
					go r.snapPNG()
				case 'q': // Quit session
					r.ptmx.Write([]byte{4}) // Ctrl+D (EOF)
				case prefixKey: // Double prefix → send literal
					r.ptmx.Write([]byte{prefixKey})
				default:
					// Unknown chord — forward both
					r.ptmx.Write([]byte{prefixKey, b})
				}
			}
		}
	}
}

func (r *Recorder) relayOutput() {
	buf := make([]byte, 8192)
	for {
		n, err := r.ptmx.Read(buf)
		if err != nil {
			if err != io.EOF {
				// Expected on shell exit
			}
			return
		}

		chunk := buf[:n]

		// Write to real terminal
		os.Stdout.Write(chunk)

		// Write to session file
		r.outputFile.Write(chunk)

		// Keep in-memory buffer
		r.mu.Lock()
		r.outputBuf = append(r.outputBuf, chunk...)
		r.mu.Unlock()
	}
}

func (r *Recorder) snapPDF() {
	r.doSnap("pdf")
}

func (r *Recorder) snapPNG() {
	r.doSnap("png")
}

func (r *Recorder) doSnap(format string) {
	// Freeze current buffer
	r.mu.Lock()
	data := make([]byte, len(r.outputBuf))
	copy(data, r.outputBuf)
	r.mu.Unlock()

	// Flush file
	r.outputFile.Sync()
	r.session.Save()

	ts := time.Now().Format("2006-01-02_15-04-05")
	outputPath := defaultOutputPath(fmt.Sprintf("recap-%s.%s", ts, format))

	// Notification (raw mode needs \r\n)
	fmt.Fprintf(os.Stderr, "\r\n\033[90m[recap]\033[0m Exporting %s...\r\n", format)

	var err error
	if format == "png" {
		err = RenderSessionPNG(r.session, outputPath, data)
	} else {
		err = RenderSessionPDF(r.session, outputPath, data)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\r\n\033[31m[recap] export failed:\033[0m %v\r\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "\r\n\033[32m[recap] ✓\033[0m %s\r\n", outputPath)
	openFile(outputPath)
}

func padRight(s string, length int) string {
	if len(s) >= length {
		return s[:length]
	}
	return s + spaces(length-len(s))
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}
