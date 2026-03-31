//go:build darwin

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func makeSyntheticPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 0x22, G: 0x55, B: 0x88, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode failed: %v", err)
	}
	return buf.Bytes()
}

func TestScreenshotAspectRatioPreserved(t *testing.T) {
	cases := []struct {
		logicalW, logicalH int
		physicalW, physicalH int
	}{
		{640, 480, 1280, 960},
		{1200, 800, 2400, 1600},
		{1920, 1080, 3840, 2160},
		{800, 1200, 1600, 2400},
		{2560, 600, 5120, 1200},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%dx%d", tc.logicalW, tc.logicalH), func(t *testing.T) {
			pngData := makeSyntheticPNG(t, tc.physicalW, tc.physicalH)

			result := &CaptureResult{
				Window: WindowInfo{
					Owner:  "Test",
					Name:   "test-window",
					Width:  tc.logicalW,
					Height: tc.logicalH,
				},
				ContentType: ContentScreenshot,
				Data:        pngData,
				Title:       "test",
			}

			htmlStr, err := buildCaptureHTML(result)
			if err != nil {
				t.Fatalf("buildCaptureHTML returned error: %v", err)
			}

			// Should use logical width, NOT physical PNG width
			expected := fmt.Sprintf("width: %dpx;", tc.logicalW)
			if !strings.Contains(htmlStr, expected) {
				t.Errorf("expected CSS %q in HTML, got html containing neither", expected)
			}

			// Should NOT contain physical width
			physical := fmt.Sprintf("width: %dpx;", tc.physicalW)
			if strings.Contains(htmlStr, physical) {
				t.Errorf("HTML should not contain physical pixel width %q", physical)
			}
		})
	}
}

func TestCanvasWidthAccountsForPadding(t *testing.T) {
	if got := canvasWidth(800); got != 864 {
		t.Errorf("canvasWidth(800) = %d, want 864", got)
	}
	if got := canvasWidth(0); got != 0 {
		t.Errorf("canvasWidth(0) = %d, want 0", got)
	}
	if got := canvasWidth(-1); got != 0 {
		t.Errorf("canvasWidth(-1) = %d, want 0", got)
	}
}

func TestBuildCaptureHTMLFallsBackToPngWidth(t *testing.T) {
	pngData := makeSyntheticPNG(t, 1600, 1200)

	result := &CaptureResult{
		Window: WindowInfo{
			Owner: "Test",
			Name:  "test-window",
			// Width/Height intentionally 0 — simulate pipe/stdin
		},
		ContentType: ContentScreenshot,
		Data:        pngData,
		Title:       "test",
	}

	htmlStr, err := buildCaptureHTML(result)
	if err != nil {
		t.Fatalf("buildCaptureHTML returned error: %v", err)
	}

	// Should fall back to pngWidth (1600)
	if !strings.Contains(htmlStr, "width: 1600px;") {
		t.Errorf("expected pngWidth fallback (1600px) when WindowInfo.Width is 0, html=%q", htmlStr)
	}
}

func TestMultiImageHTMLUsesLogicalWidth(t *testing.T) {
	img := makeSyntheticPNG(t, 1600, 1200) // 2× physical pixels

	htmlStr, err := buildMultiImageHTML("test", [][]byte{img}, nil, WindowInfo{
		Owner:  "Test",
		Name:   "test-window",
		Width:  800,
		Height: 600,
	})
	if err != nil {
		t.Fatalf("buildMultiImageHTML returned error: %v", err)
	}

	if !strings.Contains(htmlStr, "width: 800px;") {
		t.Errorf("expected logical width (800px) in HTML")
	}
	if strings.Contains(htmlStr, "width: 1600px;") {
		t.Errorf("should not use physical pngWidth (1600px) when logical width is set")
	}
}

func TestAspectRatioSlidingProperty(t *testing.T) {
	// Table-driven test across a range of window sizes
	sizes := []struct {
		logicalW, logicalH int
	}{
		{320, 240},
		{640, 480},
		{800, 600},
		{1024, 768},
		{1280, 720},
		{1920, 1080},
		{800, 1200}, // portrait
		{2560, 600}, // ultra-wide
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("%dx%d", sz.logicalW, sz.logicalH), func(t *testing.T) {
			// Simulate 2× Retina PNG
			physW, physH := sz.logicalW*2, sz.logicalH*2
			pngData := makeSyntheticPNG(t, physW, physH)

			result := &CaptureResult{
				Window: WindowInfo{
					Owner:  "Test",
					Name:   "test",
					Width:  sz.logicalW,
					Height: sz.logicalH,
				},
				ContentType: ContentScreenshot,
				Data:        pngData,
				Title:       "test",
			}

			htmlStr, err := buildCaptureHTML(result)
			if err != nil {
				t.Fatalf("buildCaptureHTML returned error: %v", err)
			}

			// CSS width must equal logical width
			expected := fmt.Sprintf("width: %dpx;", sz.logicalW)
			if !strings.Contains(htmlStr, expected) {
				t.Errorf("CSS width should be %dpx (logical), not %dpx (physical)", sz.logicalW, physW)
			}
		})
	}
}

func TestPngSize(t *testing.T) {
	data := makeSyntheticPNG(t, 1600, 1200)
	w, h := pngSize(data)
	if w != 1600 || h != 1200 {
		t.Errorf("pngSize() = (%d, %d), want (1600, 1200)", w, h)
	}

	// Invalid data
	w, h = pngSize([]byte("not a png"))
	if w != 0 || h != 0 {
		t.Errorf("pngSize(invalid) = (%d, %d), want (0, 0)", w, h)
	}
}
