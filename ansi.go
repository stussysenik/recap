package main

import (
	"fmt"
	"html"
	"strconv"
	"strings"
)

// Catppuccin Mocha palette — gorgeous dark theme
var ansiColors = [16]string{
	"#45475a", // 0  Black
	"#f38ba8", // 1  Red
	"#a6e3a1", // 2  Green
	"#f9e2af", // 3  Yellow
	"#89b4fa", // 4  Blue
	"#cba6f7", // 5  Magenta
	"#94e2d5", // 6  Cyan
	"#bac2de", // 7  White
	"#585b70", // 8  Bright Black
	"#f38ba8", // 9  Bright Red
	"#a6e3a1", // 10 Bright Green
	"#f9e2af", // 11 Bright Yellow
	"#89b4fa", // 12 Bright Blue
	"#f5c2e7", // 13 Bright Magenta
	"#94e2d5", // 14 Bright Cyan
	"#a6adc8", // 15 Bright White
}

type ansiStyle struct {
	bold      bool
	dim       bool
	italic    bool
	underline bool
	strikethrough bool
	fgColor   string
	bgColor   string
}

func (s *ansiStyle) toCSS() string {
	var parts []string
	if s.bold {
		parts = append(parts, "font-weight:700")
	}
	if s.dim {
		parts = append(parts, "opacity:0.6")
	}
	if s.italic {
		parts = append(parts, "font-style:italic")
	}
	if s.underline {
		parts = append(parts, "text-decoration:underline")
	}
	if s.strikethrough {
		if s.underline {
			parts = append(parts, "text-decoration:underline line-through")
		} else {
			parts = append(parts, "text-decoration:line-through")
		}
	}
	if s.fgColor != "" {
		parts = append(parts, "color:"+s.fgColor)
	}
	if s.bgColor != "" {
		parts = append(parts, "background:"+s.bgColor+";padding:1px 2px;border-radius:2px")
	}
	return strings.Join(parts, ";")
}

func (s *ansiStyle) isEmpty() bool {
	return !s.bold && !s.dim && !s.italic && !s.underline && !s.strikethrough &&
		s.fgColor == "" && s.bgColor == ""
}

func (s *ansiStyle) reset() {
	*s = ansiStyle{}
}

// ANSIToHTML converts raw terminal output with ANSI escape codes to styled HTML.
// Handles SGR (colors/styles), strips cursor movement, handles CR/LF properly.
func ANSIToHTML(input []byte) string {
	var out strings.Builder
	var style ansiStyle
	spanOpen := false
	var textBuf []byte

	out.Grow(len(input) * 2) // pre-allocate

	flushText := func() {
		if len(textBuf) > 0 {
			out.WriteString(html.EscapeString(string(textBuf)))
			textBuf = textBuf[:0]
		}
	}

	applyStyle := func() {
		flushText()
		if spanOpen {
			out.WriteString("</span>")
			spanOpen = false
		}
		css := style.toCSS()
		if css != "" {
			fmt.Fprintf(&out, `<span style="%s">`, css)
			spanOpen = true
		}
	}

	i := 0
	n := len(input)

	for i < n {
		b := input[i]

		// ── ESC sequences ──
		if b == 0x1B && i+1 < n {
			// CSI: ESC [
			if input[i+1] == '[' {
				j := i + 2
				// Collect parameter bytes (0x30–0x3F)
				paramStart := j
				for j < n && input[j] >= 0x30 && input[j] <= 0x3F {
					j++
				}
				params := string(input[paramStart:j])
				// Intermediate bytes (0x20–0x2F)
				for j < n && input[j] >= 0x20 && input[j] <= 0x2F {
					j++
				}
				// Final byte (0x40–0x7E)
				if j < n && input[j] >= 0x40 && input[j] <= 0x7E {
					finalByte := input[j]
					j++

					if finalByte == 'm' {
						// SGR — Select Graphic Rendition
						parseSGR(&style, params)
						applyStyle()
					}
					// All other CSI sequences (cursor movement, erase, etc.) are stripped
					i = j
					continue
				}
				// Malformed CSI — skip ESC [
				i = j
				continue
			}

			// OSC: ESC ]
			if input[i+1] == ']' {
				j := i + 2
				for j < n {
					if input[j] == 0x07 { // BEL terminates OSC
						j++
						break
					}
					if input[j] == 0x1B && j+1 < n && input[j+1] == '\\' { // ST terminates OSC
						j += 2
						break
					}
					j++
				}
				i = j
				continue
			}

			// Other ESC sequences (ESC (, ESC ), etc.) — skip 2 bytes
			i += 2
			continue
		}

		// ── Carriage return ──
		if b == '\r' {
			if i+1 < n && input[i+1] == '\n' {
				// \r\n → newline
				flushText()
				out.WriteByte('\n')
				i += 2
				continue
			}
			// Standalone \r — skip (line overwrite handling is lossy for v1)
			i++
			continue
		}

		// ── Newline ──
		if b == '\n' {
			flushText()
			out.WriteByte('\n')
			i++
			continue
		}

		// ── Tab ──
		if b == '\t' {
			flushText()
			out.WriteString("    ")
			i++
			continue
		}

		// ── Other control chars ──
		if b < 0x20 {
			i++
			continue
		}

		// ── Regular bytes (accumulate for UTF-8 safety) ──
		textBuf = append(textBuf, b)
		i++
	}

	flushText()
	if spanOpen {
		out.WriteString("</span>")
	}

	return out.String()
}

func parseSGR(style *ansiStyle, params string) {
	if params == "" || params == "0" {
		style.reset()
		return
	}

	codes := strings.Split(params, ";")
	for i := 0; i < len(codes); i++ {
		code, _ := strconv.Atoi(codes[i])

		switch {
		case code == 0:
			style.reset()
		case code == 1:
			style.bold = true
		case code == 2:
			style.dim = true
		case code == 3:
			style.italic = true
		case code == 4:
			style.underline = true
		case code == 9:
			style.strikethrough = true
		case code == 22:
			style.bold = false
			style.dim = false
		case code == 23:
			style.italic = false
		case code == 24:
			style.underline = false
		case code == 29:
			style.strikethrough = false

		// Standard foreground colors
		case code >= 30 && code <= 37:
			style.fgColor = ansiColors[code-30]
		case code == 38:
			// Extended foreground: 38;5;n (256) or 38;2;r;g;b (truecolor)
			if i+1 < len(codes) {
				mode, _ := strconv.Atoi(codes[i+1])
				if mode == 5 && i+2 < len(codes) {
					idx, _ := strconv.Atoi(codes[i+2])
					style.fgColor = color256ToHex(idx)
					i += 2
				} else if mode == 2 && i+4 < len(codes) {
					r, _ := strconv.Atoi(codes[i+2])
					g, _ := strconv.Atoi(codes[i+3])
					b, _ := strconv.Atoi(codes[i+4])
					style.fgColor = fmt.Sprintf("#%02x%02x%02x", r, g, b)
					i += 4
				}
			}
		case code == 39:
			style.fgColor = ""

		// Standard background colors
		case code >= 40 && code <= 47:
			style.bgColor = ansiColors[code-40]
		case code == 48:
			// Extended background
			if i+1 < len(codes) {
				mode, _ := strconv.Atoi(codes[i+1])
				if mode == 5 && i+2 < len(codes) {
					idx, _ := strconv.Atoi(codes[i+2])
					style.bgColor = color256ToHex(idx)
					i += 2
				} else if mode == 2 && i+4 < len(codes) {
					r, _ := strconv.Atoi(codes[i+2])
					g, _ := strconv.Atoi(codes[i+3])
					b, _ := strconv.Atoi(codes[i+4])
					style.bgColor = fmt.Sprintf("#%02x%02x%02x", r, g, b)
					i += 4
				}
			}
		case code == 49:
			style.bgColor = ""

		// Bright foreground
		case code >= 90 && code <= 97:
			style.fgColor = ansiColors[code-90+8]
		// Bright background
		case code >= 100 && code <= 107:
			style.bgColor = ansiColors[code-100+8]
		}
	}
}

func color256ToHex(idx int) string {
	if idx < 0 {
		idx = 0
	}
	if idx < 16 {
		return ansiColors[idx]
	}
	if idx < 232 {
		// 6×6×6 color cube
		idx -= 16
		b := idx % 6
		idx /= 6
		g := idx % 6
		r := idx / 6
		return fmt.Sprintf("#%02x%02x%02x", cubeVal(r), cubeVal(g), cubeVal(b))
	}
	if idx < 256 {
		// Grayscale ramp
		v := (idx-232)*10 + 8
		return fmt.Sprintf("#%02x%02x%02x", v, v, v)
	}
	return ""
}

func cubeVal(i int) int {
	if i == 0 {
		return 0
	}
	return 55 + i*40
}
