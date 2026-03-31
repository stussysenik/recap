//go:build darwin

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestCropBottomBlankRows(t *testing.T) {
	data := encodeRowImage(t, 8, append(
		repeatRow(color.NRGBA{R: 0x22, G: 0x88, B: 0xaa, A: 0xff}, 8),
		repeatRow(color.NRGBA{R: 0x11, G: 0x11, B: 0x1b, A: 0xff}, 12)...,
	)...)

	cropped, err := cropBottomBlankRows(data)
	if err != nil {
		t.Fatalf("cropBottomBlankRows returned error: %v", err)
	}

	if got := decodePNGHeight(t, cropped); got != 8 {
		t.Fatalf("cropped height = %d, want 8", got)
	}
}

func TestTrimBottomPaddingAfterOverlap(t *testing.T) {
	imgA := encodeRowImage(t, 8, append(
		repeatRow(color.NRGBA{R: 0x55, G: 0x22, B: 0x22, A: 0xff}, 12),
		repeatRow(color.NRGBA{R: 0x22, G: 0x55, B: 0x88, A: 0xff}, 12)...,
	)...)
	imgB := encodeRowImage(t, 8, append(
		repeatRow(color.NRGBA{R: 0x22, G: 0x55, B: 0x88, A: 0xff}, 12),
		append(
			repeatRow(color.NRGBA{R: 0x88, G: 0x88, B: 0x22, A: 0xff}, 6),
			repeatRow(color.NRGBA{R: 0x11, G: 0x11, B: 0x1b, A: 0xff}, 12)...,
		)...,
	)...)

	trimmed, err := trimOverlap([][]byte{imgA, imgB})
	if err != nil {
		t.Fatalf("trimOverlap returned error: %v", err)
	}

	trimmed = trimBottomPadding(trimmed)
	if len(trimmed) != 2 {
		t.Fatalf("trimmed image count = %d, want 2", len(trimmed))
	}

	if got := decodePNGHeight(t, trimmed[1]); got != 6 {
		t.Fatalf("trimmed second image height = %d, want 6", got)
	}
}

func TestBuildMultiImageHTMLIncludesSearchTextAndNativeWidth(t *testing.T) {
	img := encodeRowImage(t, 8, repeatRow(color.NRGBA{R: 0x22, G: 0x55, B: 0x88, A: 0xff}, 6)...)

	// With WindowInfo.Width set, logical width should be used instead of pngWidth.
	htmlStr, err := buildMultiImageHTML("ghostty", [][]byte{img}, []byte("copy me"), WindowInfo{
		Owner:  "Ghostty",
		Name:   "pane",
		Width:  400,
		Height: 300,
	})
	if err != nil {
		t.Fatalf("buildMultiImageHTML returned error: %v", err)
	}

	if !strings.Contains(htmlStr, `class="copy-layer"`) {
		t.Fatalf("expected copy-layer markup in rendered HTML")
	}
	if !strings.Contains(htmlStr, "copy me") {
		t.Fatalf("expected search text in rendered HTML")
	}
	// Should use logical width (400), not pngWidth (8)
	if !strings.Contains(htmlStr, "width: 400px;") {
		t.Fatalf("expected logical window width (400px) in rendered HTML, html=%q", htmlStr)
	}
	if strings.Contains(htmlStr, "width: 8px;") {
		t.Fatalf("should not use pngWidth when WindowInfo.Width is set")
	}
	if strings.Contains(htmlStr, "min-height: 100%") {
		t.Fatalf("unexpected fixed min-height in rendered HTML")
	}
}

func TestBuildMultiImageHTMLFallsBackToPngWidth(t *testing.T) {
	img := encodeRowImage(t, 8, repeatRow(color.NRGBA{R: 0x22, G: 0x55, B: 0x88, A: 0xff}, 6)...)

	// With WindowInfo.Width == 0, should fall back to pngWidth
	htmlStr, err := buildMultiImageHTML("ghostty", [][]byte{img}, nil, WindowInfo{
		Owner: "Ghostty",
		Name:  "pane",
	})
	if err != nil {
		t.Fatalf("buildMultiImageHTML returned error: %v", err)
	}

	if !strings.Contains(htmlStr, "width: 8px;") {
		t.Fatalf("expected pngWidth fallback (8px) when WindowInfo.Width is 0, html=%q", htmlStr)
	}
}

func encodeRowImage(t *testing.T, width int, rows ...color.NRGBA) []byte {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, len(rows)))
	for y, row := range rows {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, row)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode failed: %v", err)
	}
	return buf.Bytes()
}

func repeatRow(c color.NRGBA, n int) []color.NRGBA {
	rows := make([]color.NRGBA, n)
	for i := range rows {
		rows[i] = c
	}
	return rows
}

func decodePNGHeight(t *testing.T, data []byte) int {
	t.Helper()

	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.DecodeConfig failed: %v", err)
	}
	return cfg.Height
}
