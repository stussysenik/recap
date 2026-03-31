# recap — Progress Log

## v0.4.0 — PNG Optimization & Window Targeting

### Direct PNG Stitching (Chrome bypass)
- Scroll-captured screenshots now stitch directly in Go via `image/png` + `image/draw`
- Eliminates Chrome dependency for PNG output entirely
- Previously: Chrome rendered base64-embedded HTML → timeout at 30s for large captures
- Now: direct pixel stitching, no intermediate HTML or base64 encoding

### Median-Cut Color Quantization
- Implements Heckbert (1982) median-cut algorithm for optimal 256-color palette
- Samples 25% of pixels across all pages for histogram
- Floyd-Steinberg dithering for smooth anti-aliased terminal text
- Output: 8-bit indexed palette PNG instead of 32-bit RGBA
- Result: **15MB for 12 Retina pages** (was 231MB with naive RGBA for 200 pages)

### BestCompression PNG Encoder
- Replaced `png.Encode()` (default compression) with `png.Encoder{CompressionLevel: png.BestCompression}`
- Applied to both direct stitching path and Chrome rendering fallback

### Precise Window Targeting
- `--window-id N` — target exact macOS window ID (from `detect --list`)
- `--title PATTERN` — substring match on window title
- `--pane N` — capture only pane N (1-indexed)
- Interactive picker when multiple Ghostty windows found (no more silent first-window selection)
- Error messages list available windows with IDs for quick retargeting

### Scaled Chrome Timeout (PDF path)
- Timeout scales with HTML content size: `30s base + 15s per MB`
- Capped at 5 minutes
- Prevents "context canceled" on large multi-page PDF renders

## v0.3.1 — cmux Native Integration

- Discover cmux workspaces and surfaces via `cmux tree --all --json`
- Full scrollback text capture via `cmux read-screen` — no screenshot stitching
- cmux Workspaces group in TUI selector with `[text]` badges
- `(this shell)` marker for the current cmux surface
- Window filtering to exclude cmux-managed terminal windows from duplicating
- Version bump to 0.3.1

## v0.3.0 — Window Detection & Interactive Capture

- `recap detect` — discover all visible terminal and browser windows via macOS APIs
- `recap detect --list` — detailed list view with window metadata
- Interactive TUI selector with grouped categories (Terminals, Browsers, tmux Panes)
- Ghostty split pane detection via Accessibility API (each pane captured separately)
- Scroll-stitch capture for windows taller than one screen
- tmux pane discovery and capture across all sessions
- Cross-Space window detection on macOS (windows on all Spaces/Desktops)
- Concurrent capture pipeline for parallel multi-source rendering

## v0.2.0 — Claude Code & Grab

- `recap claude` — render Claude Code JSONL conversation logs as PDFs
- `recap grab` — capture scrollback from tmux, clipboard, or files
- `recap grab --edit` — capture → open in `$EDITOR` → trim → render
- `recap screen` — screenshot terminal window as PDF
- `recap screen --pages` — multi-page screenshot capture
- Auto-detection of grab source (tmux/clipboard/file)

## v0.1.0 — Initial Release

- PTY-based session recording with `recap record`
- In-session shortcuts: `Ctrl+]` then `s` (PDF), `p` (PNG), `q` (quit)
- `recap pipe` — render stdin as PDF/PNG
- `recap snap` — export recorded sessions
- `recap list` — list all recorded sessions
- Catppuccin Mocha themed output with full ANSI color support
- PDF and PNG output formats
- macOS Screen Recording permission integration
