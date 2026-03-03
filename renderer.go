package main

import (
	"context"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const sessionHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<style>
  @page {
    size: A4;
    margin: 0;
  }

  *, *::before, *::after {
    margin: 0;
    padding: 0;
    box-sizing: border-box;
  }

  html, body {
    background: #11111b;
    width: 100%;
    min-height: 100%;
  }

  body {
    padding: 32px;
    font-family:
      'JetBrains Mono', 'SF Mono', 'Fira Code', 'Cascadia Code',
      'Menlo', 'Monaco', 'Consolas', 'Liberation Mono', monospace;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
  }

  .window {
    background: #1e1e2e;
    border-radius: 12px;
    overflow: hidden;
    box-shadow:
      0 0 0 1px rgba(205, 214, 244, 0.06),
      0 4px 6px rgba(0, 0, 0, 0.15),
      0 25px 50px -12px rgba(0, 0, 0, 0.5),
      0 0 120px -40px rgba(137, 180, 250, 0.08);
  }

  /* ── Title Bar ── */
  .titlebar {
    background: #181825;
    padding: 14px 20px;
    display: flex;
    align-items: center;
    border-bottom: 1px solid rgba(205, 214, 244, 0.06);
    user-select: none;
  }

  .dots {
    display: flex;
    gap: 8px;
    margin-right: 16px;
    flex-shrink: 0;
  }

  .dot {
    width: 12px;
    height: 12px;
    border-radius: 50%;
  }
  .dot.close    { background: #f38ba8; }
  .dot.minimize { background: #f9e2af; }
  .dot.maximize { background: #a6e3a1; }

  .title {
    color: #6c7086;
    font-size: 12px;
    font-weight: 500;
    letter-spacing: 0.02em;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1;
  }

  .badge {
    color: #45475a;
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    flex-shrink: 0;
    margin-left: 12px;
  }

  /* ── Content ── */
  .content {
    padding: 20px 24px;
    color: #cdd6f4;
    font-size: 11px;
    line-height: 1.65;
    white-space: pre-wrap;
    word-wrap: break-word;
    word-break: break-all;
    tab-size: 4;
    overflow-wrap: anywhere;
  }

  /* ── Footer ── */
  .footer {
    padding: 12px 24px;
    border-top: 1px solid rgba(205, 214, 244, 0.06);
    display: flex;
    justify-content: space-between;
    align-items: center;
    color: #45475a;
    font-size: 10px;
  }

  .footer .meta {
    display: flex;
    gap: 16px;
  }

  .footer .meta span {
    display: flex;
    align-items: center;
    gap: 4px;
  }

  .footer .brand {
    color: #585b70;
    font-weight: 700;
    letter-spacing: 0.1em;
    text-transform: uppercase;
    font-size: 9px;
  }

  /* ── Scrollbar (for PNG full-page capture) ── */
  ::-webkit-scrollbar { width: 0; height: 0; }

  /* ── Selection styling ── */
  ::selection {
    background: rgba(137, 180, 250, 0.3);
    color: #cdd6f4;
  }

  /* ── Print overrides ── */
  @media print {
    body { padding: 16px; }
    .window {
      box-shadow: none;
      border: 1px solid rgba(205, 214, 244, 0.1);
    }
  }
</style>
</head>
<body>
  <div class="window">
    <div class="titlebar">
      <div class="dots">
        <span class="dot close"></span>
        <span class="dot minimize"></span>
        <span class="dot maximize"></span>
      </div>
      <span class="title">{{.Title}}</span>
      <span class="badge">recap</span>
    </div>
    <div class="content">{{.Content}}</div>
    <div class="footer">
      <div class="meta">
        <span>{{.Date}}</span>
        <span>·</span>
        <span>{{.Duration}}</span>
        <span>·</span>
        <span>{{.Directory}}</span>
      </div>
      <span class="brand">recap v{{.Version}}</span>
    </div>
  </div>
</body>
</html>`

type renderData struct {
	Title     string
	Content   template.HTML
	Date      string
	Duration  string
	Directory string
	Version   string
}

func buildHTML(sess *Session, data []byte) (string, error) {
	htmlContent := ANSIToHTML(data)

	duration := "active"
	if !sess.End.IsZero() {
		d := sess.End.Sub(sess.Start).Round(time.Second)
		duration = d.String()
	}

	rd := renderData{
		Title:     fmt.Sprintf("recap — %s", filepath.Base(sess.CWD)),
		Content:   template.HTML(htmlContent),
		Date:      sess.Start.Format("2006-01-02 15:04"),
		Duration:  duration,
		Directory: sess.CWD,
		Version:   version,
	}

	tmpl, err := template.New("session").Parse(sessionHTML)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, rd); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

func writeHTMLTmp(htmlStr string) (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "recap-render-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	htmlPath := filepath.Join(tmpDir, "session.html")
	if err := os.WriteFile(htmlPath, []byte(htmlStr), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return htmlPath, cleanup, nil
}

// RenderSessionPDF generates a paginated PDF from session data.
func RenderSessionPDF(sess *Session, outputPath string, data []byte) error {
	htmlStr, err := buildHTML(sess, data)
	if err != nil {
		return err
	}

	htmlPath, cleanup, err := writeHTMLTmp(htmlStr)
	if err != nil {
		return err
	}
	defer cleanup()

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var buf []byte
	err = chromedp.Run(ctx,
		chromedp.Navigate("file://"+htmlPath),
		chromedp.WaitReady("body"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			buf, _, err = page.PrintToPDF().
				WithPrintBackground(true).
				WithPreferCSSPageSize(true).
				WithMarginTop(0).
				WithMarginBottom(0).
				WithMarginLeft(0).
				WithMarginRight(0).
				Do(ctx)
			return err
		}),
	)
	if err != nil {
		return fmt.Errorf("rendering PDF: %w", err)
	}

	return os.WriteFile(outputPath, buf, 0644)
}

// RenderSessionPNG generates a single continuous PNG screenshot from session data.
func RenderSessionPNG(sess *Session, outputPath string, data []byte) error {
	htmlStr, err := buildHTML(sess, data)
	if err != nil {
		return err
	}

	htmlPath, cleanup, err := writeHTMLTmp(htmlStr)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.WindowSize(1200, 800),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var buf []byte
	err = chromedp.Run(ctx,
		chromedp.Navigate("file://"+htmlPath),
		chromedp.WaitReady("body"),
		chromedp.FullScreenshot(&buf, 100),
	)
	if err != nil {
		return fmt.Errorf("rendering PNG: %w", err)
	}

	return os.WriteFile(outputPath, buf, 0644)
}

// openFile opens a file with the system default application.
func openFile(path string) {
	var cmd string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	default:
		return
	}
	exec.Command(cmd, path).Start()
}
