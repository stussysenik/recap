```
  ┌──────────────────────────────────────────────────┐
  │  ◉ recap — terminal output as beautiful PDFs     │
  └──────────────────────────────────────────────────┘
```

capture terminal scrollback, app windows, tmux panes, cmux workspaces, or raw sessions — and render them as themed PDFs or optimized PNGs. one binary, no dependencies, works with what you already use.

## quick start

![Demo](demo.gif)


```bash
git clone https://github.com/stussysenik/recap.git
cd recap && make install
recap
```

## what's new in v0.5.0

- Ghostty PDFs can now capture exact split scrollback headlessly via `recap chat --title`, `--tab`, and `--active-pane`.
- `recap detect` now supports direct exact targeting with `--app`, `--title`, `--window-id`, `--active`, and `--active-window`.
- Zed windows that host a live Codex terminal now export the real transcript top-to-bottom instead of a screenshot of the editor surface.
- exports default to `~/Downloads`, and `--no-open` keeps background/headless capture flows quiet.

see also: [VISION.md](VISION.md), [PROGRESS.md](PROGRESS.md), and [CHANGELOG.md](CHANGELOG.md).

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
recap chat --title "ipod-classic-anniversary-validation"  fastest exact Ghostty split → searchable PDF
recap chat --active --active-pane                         fastest focused Ghostty split → searchable PDF
recap chat --title "ipod-classic-anniversary-validation" --no-open  headless background-friendly Ghostty PDF
recap chat --tab "validation"                             most stable named Ghostty tab
recap detect --active-window                             fastest focused app/editor window
recap detect --app zed --title "settings.json"           exact Zed/editor window
recap detect --app zed --title "expense-os"              exact Zed/Codex terminal transcript when available
recap detect --window-id 14995                           exact any-app window ID
recap chat --pane 1          capture only pane N (1-indexed)
recap chat --tab "project"   target a named Ghostty tab
recap chat --title "project" target Ghostty split title first, else window title
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

**Ghostty** — split panes detected automatically via Accessibility API. on Ghostty 1.3+, PDFs prefer a headless path: `recap` resolves the exact terminal via Ghostty's AppleScript model and exports scrollback without clicking, moving the cursor, or bringing Ghostty to the front. `--title` first tries the current tab's split titles, then falls back to the window title. `--active-pane` targets the focused split in the selected tab. `--tab` is still the most stable selector if you manually name Ghostty tabs.

**Zed / editors / desktop apps** — fastest path is `recap detect --active-window` after focusing the window you want. most precise path is `recap detect --app <name> --title "<substring>"` or `recap detect --window-id <id>` from `recap detect --list`. for terminal windows, `recap` now prefers headless content export before any screenshot fallback. when the selected editor window owns a live Codex terminal, `recap` exports the Codex rollout transcript headlessly instead of screenshotting the editor surface, so `recap detect --app zed --title "expense-os"` produces real terminal content top-to-bottom. other editor terminals still fall back to window capture unless the app exposes its own export path.

**tmux** — all sessions and panes discovered automatically. scrollback captured with full ANSI color sequences preserved.

**cmux** — workspaces and surfaces discovered via socket at `/tmp/cmux.sock`. full scrollback text capture — no screenshot stitching needed.

**Claude Code** — `recap claude` renders JSONL conversation logs with role-based formatting (Human/Assistant/Tool).

## terminals

Ghostty · iTerm2 · Terminal.app · Alacritty · Kitty · WezTerm · any window visible to macOS

## flags

```
--png              output PNG instead of PDF
--output=PATH      custom output path
--no-open          do not open the rendered file after capture
--edit, -e         open in $EDITOR before rendering
--title=TEXT       target window by title / custom header title
--app=TEXT         target app/window owner by substring
--window-id=N      target exact window ID
--active-window    target the focused app window
--pane=N           capture only pane N (1-indexed)
```

default output location is `~/Downloads` unless you pass `--output`.

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

> **permissions** — recap needs Screen Recording (for window detection), Accessibility (for Ghostty split panes), and macOS may prompt once for Automation when `recap` controls Ghostty's AppleScript API. grant in System Settings → Privacy & Security.

## output

all output uses the **Catppuccin Mocha** color theme — full 256-color and truecolor ANSI, monospace rendering, automatic page breaks, timestamped headers.

---

v0.5.0 · MIT
