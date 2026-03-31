# recap — Roadmap

## Shipped

- [x] PTY session recording with in-session shortcuts
- [x] `recap pipe` — stdin to PDF/PNG
- [x] `recap grab` — tmux/clipboard/file scrollback capture
- [x] `recap detect` — window detection with TUI selector
- [x] Ghostty split pane detection via Accessibility API
- [x] Scroll-stitch capture for full scrollback
- [x] tmux pane discovery across all sessions
- [x] cmux workspace/surface integration
- [x] Kitty socket protocol integration
- [x] Claude Code JSONL log rendering
- [x] Direct PNG stitching (Chrome bypass)
- [x] Median-cut color quantization (Heckbert 1982)
- [x] Precise window targeting (`--window-id`, `--title`, `--pane`)
- [x] Interactive window picker for multi-window setups

## Next

- [ ] `--1x` flag — downscale Retina 2x captures to 1x logical pixels (4x size reduction)
- [ ] WebP output format — typically 30-40% smaller than PNG for terminal content
- [ ] Per-page streaming encoder — avoid holding full canvas in memory for very large captures
- [ ] `recap watch` — auto-capture on terminal activity (inotify/kqueue)
- [ ] Linux support — X11/Wayland window detection + screenshot capture
- [ ] `recap diff` — visual diff between two captures
- [ ] Shell integration auto-capture — hook into prompt to snapshot each command's output

## Ideas

- [ ] Zig-accelerated image pipeline via CGo — SIMD color quantization and PNG encoding
- [ ] Adaptive palette sizing — use fewer colors when content allows (terminal without anti-aliasing → 32 colors → smaller files)
- [ ] AVIF output — next-gen compression, ~50% smaller than WebP
- [ ] Browser extension — capture terminal-in-browser (VS Code, web SSH)
- [ ] Team sharing — upload captures to a shared URL with `recap share`
