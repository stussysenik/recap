```
  ┌──────────────────────────────────────────────────┐
  │  ◉ recap — terminal output as beautiful PDFs     │
  └──────────────────────────────────────────────────┘
```

capture any terminal — windows, tmux panes, cmux workspaces, or raw sessions — and render them as themed PDFs or optimized PNGs. one binary, no dependencies, works with what you already use.

## quick start

![Demo](demo.gif)


```bash
git clone https://github.com/stussysenik/recap.git
cd recap && make install
recap
```

## what it captures

**windows & panes**
```
recap                    record a shell session (PTY wrapper)
recap detect             detect windows → select → capture → PDF
recap detect --list      list detected windows with details
recap chat               quick Ghostty capture (interactive picker if >1 window)
recap screen             screenshot terminal window
recap screen --pages     multi-page screenshot → stitched PDF
```

**precise targeting**
```
recap chat --pane 1          capture only pane N (1-indexed)
recap chat --title "project" target Ghostty window matching title
recap chat --window-id 53083 target exact window ID (from detect --list)
recap chat --png             pixel-perfect PNG with palette quantization
```

**sessions & scrollback**
```
recap grab               capture scrollback (tmux/clipboard/file)
recap grab --edit        capture → open in $EDITOR → trim → render
recap grab --from=tmux   tmux scrollback with ANSI colors
recap grab --from=clipboard   macOS clipboard (pbpaste)
recap grab --from=<file>      read from file
```

**export**
```
recap snap               export last recorded session
recap snap --png         export as PNG instead of PDF
recap list               list recorded sessions
```

## pipe everything

```bash
pbpaste | recap pipe                        # clipboard → PDF
cat session.log | recap pipe                # file → PDF
tmux capture-pane -pS- -e | recap pipe      # tmux → PDF
command 2>&1 | recap pipe --png             # command → PNG
```

## in-session shortcuts

during `recap` recording:

```
Ctrl+] then s         snap → PDF
Ctrl+] then p         snap → PNG
Ctrl+] then q         quit recording
Ctrl+] Ctrl+]         send literal Ctrl+]
```

## plays nice with

**Ghostty** — split panes detected automatically via Accessibility API. each pane captured separately. precise targeting with `--pane`, `--title`, `--window-id`. interactive picker when multiple windows found.

**tmux** — all sessions and panes discovered automatically. scrollback captured with full ANSI color sequences preserved.

**cmux** — workspaces and surfaces discovered via socket at `/tmp/cmux.sock`. full scrollback text capture — no screenshot stitching needed.

**Claude Code** — `recap claude` renders JSONL conversation logs with role-based formatting (Human/Assistant/Tool).

## terminals

Ghostty · iTerm2 · Terminal.app · Alacritty · Kitty · WezTerm · any window visible to macOS

## flags

```
--png              output PNG instead of PDF
--output=PATH      custom output path
--edit, -e         open in $EDITOR before rendering
--title=TEXT       target window by title / custom header title
--window-id=N      target exact window ID
--pane=N           capture only pane N (1-indexed)
```

## image optimization

PNG output uses **median-cut color quantization** (Heckbert 1982) to produce 8-bit indexed palette PNGs. terminal screenshots compress dramatically — typically **15MB for 12 Retina pages** vs 231MB with naive RGBA encoding. the pipeline:

1. scroll-capture each page as pixel-perfect Retina screenshots
2. sample color histogram across all pages (25% of pixels)
3. median-cut partitioning to optimal 256-color palette
4. Floyd-Steinberg dithering for smooth anti-aliased text
5. PNG BestCompression encoding

no Chrome dependency for PNG output — screenshots stitch directly in Go.

## install

```bash
git clone https://github.com/stussysenik/recap.git
cd recap && make install
```

installs to `~/.local/bin/`. for a different prefix:

```bash
make install PREFIX=/usr/local
```

requires Go 1.21+ and macOS.

> **permissions** — recap needs Screen Recording (for window detection) and Accessibility (for Ghostty split panes). grant in System Settings → Privacy & Security.

## output

all output uses the **Catppuccin Mocha** color theme — full 256-color and truecolor ANSI, monospace rendering, automatic page breaks, timestamped headers.

---

v0.4.0 · MIT
