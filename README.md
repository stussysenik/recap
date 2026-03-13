# recap

Terminal output as beautiful PDFs. Capture any terminal — windows, tmux panes, cmux workspaces, or raw sessions — and render them as Catppuccin Mocha themed PDFs with full ANSI color support.

## Features

- **Record** — PTY-based session recording with in-session snap shortcuts
- **Detect** — Discover all terminal windows, tmux panes, and cmux workspaces automatically
- **Grab** — Capture scrollback from tmux, clipboard, or files
- **Pipe** — Render any stdin stream as a PDF or PNG
- **Screen** — Screenshot terminal windows with multi-page stitching
- **Claude** — Render Claude Code JSONL conversation logs

## Capture Sources

| Source | Method | Output |
|--------|--------|--------|
| Terminal windows | Screenshot via macOS APIs | PDF/PNG |
| Ghostty split panes | Accessibility API per-pane capture | PDF/PNG per pane |
| tmux panes | `tmux capture-pane` with ANSI | PDF/PNG |
| cmux workspaces | `cmux read-screen` full scrollback | PDF/PNG (text) |
| PTY recording | Live session capture | PDF/PNG |
| stdin | Pipe any text/ANSI stream | PDF/PNG |

## Supported Terminals

- Ghostty (including split pane detection)
- iTerm2
- Terminal.app
- Alacritty
- Kitty
- WezTerm
- Any terminal window visible to macOS

## Supported Browsers

- Safari
- Chrome / Chromium
- Firefox
- Arc

## Installation

```bash
git clone https://github.com/stussysenik/recap.git
cd recap
go build -o recap .
```

Requires Go 1.21+ and macOS (uses macOS-specific APIs for window detection and capture).

## Usage

### Record a session

```bash
recap                    # Start recording (PTY wrapper)
# Ctrl+] then s         # Snap to PDF during recording
# Ctrl+] then q         # Quit recording
```

### Detect and capture

```bash
recap detect             # Discover windows → interactive TUI → capture → PDF
recap detect --list      # List all detected windows, tmux panes, cmux workspaces
```

### Grab scrollback

```bash
recap grab               # Auto-detect source (tmux/clipboard)
recap grab --from=tmux   # Capture tmux scrollback with ANSI colors
recap grab --edit        # Capture → open in $EDITOR → trim → render
```

### Pipe input

```bash
pbpaste | recap pipe                        # Clipboard → PDF
cat session.log | recap pipe                # File → PDF
tmux capture-pane -pS- -e | recap pipe      # tmux → PDF
command 2>&1 | recap pipe --png             # Command output → PNG
```

### Screenshot

```bash
recap screen             # Screenshot terminal window
recap screen --pages     # Multi-page screenshot capture
```

### Claude Code logs

```bash
recap claude             # Render latest Claude Code conversation
```

### Export and list

```bash
recap snap               # Export last recorded session
recap snap --png         # Export as PNG instead of PDF
recap list               # List all recorded sessions
```

## Flags

```
--png              Output PNG instead of PDF
--output=PATH      Custom output path
--edit, -e         Open in $EDITOR before rendering
--title=TEXT       Custom header title
```

## Permissions

recap requires the following macOS permissions:

- **Screen Recording** — Required for window detection and screenshot capture
- **Accessibility** — Required for Ghostty split pane detection via AX API

Grant these in: System Settings → Privacy & Security

## Integrations

### Ghostty

Split panes are detected automatically via the Accessibility API. Each pane is captured as a separate PDF with its own content.

### tmux

All tmux sessions and panes are discovered automatically. Scrollback is captured with full ANSI color sequences preserved.

### cmux

Workspaces and surfaces are discovered via the cmux socket (`/tmp/cmux.sock`). Full scrollback text is captured directly — no screenshot stitching needed.

### Claude Code

Claude Code JSONL conversation logs are parsed and rendered with role-based formatting (Human/Assistant/Tool).

## Output

All output uses the **Catppuccin Mocha** color theme with:

- Full 256-color and truecolor ANSI support
- Monospace font rendering (JetBrains Mono / system monospace)
- Automatic page breaks for long output
- Header with title, timestamp, and source info

## License

MIT
