package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"html/template"
	"image/png"
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
    {{if gt .CanvasWidth 0}}
    width: {{.CanvasWidth}}px;
    {{else}}
    width: 100%;
    {{end}}
  }

  body {
    padding: 32px;
    font-family:
      'JetBrains Mono', 'SF Mono', 'Fira Code', 'Cascadia Code',
      'Menlo', 'Monaco', 'Consolas', 'Liberation Mono', monospace;
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
    display: flex;
    justify-content: center;
    align-items: flex-start;
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
    {{if gt .WindowWidth 0}}
    width: {{.WindowWidth}}px;
    {{end}}
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

  /* ── Image capture layout ── */
  .content.image-capture,
  .content.multi-image {
    padding: 0;
    position: relative;
    overflow: hidden;
  }
  .content.image-capture img,
  .content.multi-image img {
    width: 100%;
    height: auto;
    display: block;
    margin: 0;
    padding: 0;
  }
  .copy-layer {
    position: absolute;
    inset: 0;
    z-index: 2;
    margin: 0;
    padding: 0;
    color: rgba(255, 255, 255, 0.01);
    background: transparent;
    white-space: pre-wrap;
    word-break: break-word;
    overflow-wrap: anywhere;
    tab-size: 4;
    font-size: 11px;
    line-height: 1.65;
    user-select: text;
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
    <div class="content{{if .ContentClass}} {{.ContentClass}}{{end}}">{{.Content}}</div>
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
	Title        string
	Content      template.HTML
	ContentClass string
	Date         string
	Duration     string
	Directory    string
	Version      string
	WindowWidth  int
	CanvasWidth  int
}

func pngWidth(data []byte) int {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0
	}
	return cfg.Width
}

func canvasWidth(windowWidth int) int {
	if windowWidth <= 0 {
		return 0
	}
	return windowWidth
}

func buildSearchTextLayer(searchText []byte) string {
	if len(searchText) == 0 {
		return ""
	}
	return fmt.Sprintf(`<pre class="copy-layer">%s</pre>`, html.EscapeString(string(searchText)))
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
	return renderHTMLtoPDF(htmlStr, outputPath)
}

// RenderSessionPNG generates a single continuous PNG screenshot from session data.
func RenderSessionPNG(sess *Session, outputPath string, data []byte) error {
	htmlStr, err := buildHTML(sess, data)
	if err != nil {
		return err
	}
	return renderHTMLtoPNG(htmlStr, outputPath)
}

// renderHTMLtoPDF renders an HTML string to a PDF file using chromedp.
func renderHTMLtoPDF(htmlStr, outputPath string) error {
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

// renderHTMLtoPNG renders an HTML string to a PNG file using chromedp.
func renderHTMLtoPNG(htmlStr, outputPath string) error {
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
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var buf []byte
	var docWidth int64
	err = chromedp.Run(ctx,
		chromedp.EmulateViewport(1200, 800, chromedp.EmulateScale(2)),
		chromedp.Navigate("file://"+htmlPath),
		chromedp.WaitReady("body"),
		chromedp.Evaluate(`Math.ceil(Math.max(document.documentElement.scrollWidth, document.body ? document.body.scrollWidth : 0))`, &docWidth),
	)
	if err != nil {
		return fmt.Errorf("rendering PNG: %w", err)
	}

	if docWidth < 1 {
		docWidth = 1200
	}
	if docWidth > 12000 {
		docWidth = 12000
	}

	err = chromedp.Run(ctx,
		chromedp.EmulateViewport(docWidth, 800, chromedp.EmulateScale(2)),
		chromedp.FullScreenshot(&buf, 100),
	)
	if err != nil {
		return fmt.Errorf("rendering PNG: %w", err)
	}

	return os.WriteFile(outputPath, buf, 0644)
}

// buildCaptureHTML generates HTML for a CaptureResult (from detect command).
func buildCaptureHTML(result *CaptureResult) (string, error) {
	var contentHTML string
	var contentClass string
	var windowWidth int

	switch result.ContentType {
	case ContentTextANSI:
		contentHTML = ANSIToHTML(result.Data)
	case ContentTextPlain:
		contentHTML = html.EscapeString(string(result.Data))
	case ContentScreenshot:
		b64 := base64.StdEncoding.EncodeToString(result.Data)
		contentClass = "image-capture"
		windowWidth = pngWidth(result.Data)
		contentHTML = fmt.Sprintf(
			`<img src="data:image/png;base64,%s" />%s`,
			b64,
			buildSearchTextLayer(result.SearchText),
		)
	}

	rd := renderData{
		Title:        result.Title,
		Content:      template.HTML(contentHTML),
		ContentClass: contentClass,
		Date:         time.Now().Format("2006-01-02 15:04"),
		Duration:     fmt.Sprintf("%s (%dx%d)", result.Window.Owner, result.Window.Width, result.Window.Height),
		Directory:    result.Window.Name,
		Version:      version,
		WindowWidth:  windowWidth,
		CanvasWidth:  canvasWidth(windowWidth),
	}

	tmpl, err := template.New("capture").Parse(sessionHTML)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, rd); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

// buildMultiImageHTML generates HTML for multiple stacked screenshots (scroll-stitch).
func buildMultiImageHTML(title string, images [][]byte, searchText []byte, w WindowInfo) (string, error) {
	var contentParts []string
	for _, img := range images {
		b64 := base64.StdEncoding.EncodeToString(img)
		contentParts = append(contentParts,
			fmt.Sprintf(`<img src="data:image/png;base64,%s" />`, b64))
	}
	contentHTML := strings.Join(contentParts, "\n") + buildSearchTextLayer(searchText)
	windowWidth := pngWidth(images[0])

	rd := renderData{
		Title:        title,
		Content:      template.HTML(contentHTML),
		ContentClass: "multi-image",
		Date:         time.Now().Format("2006-01-02 15:04"),
		Duration:     fmt.Sprintf("%d pages captured · %s (%dx%d)", len(images), w.Owner, w.Width, w.Height),
		Directory:    w.Name,
		Version:      version,
		WindowWidth:  windowWidth,
		CanvasWidth:  canvasWidth(windowWidth),
	}

	tmpl, err := template.New("multiimage").Parse(sessionHTML)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, rd); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
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
