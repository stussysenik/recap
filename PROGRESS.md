# recap — Progress Log

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
