# Changelog

This project uses semantic version tags and conventional commit subjects to describe shipped changes.

## v0.5.0 - 2026-04-03

### Added
- headless Ghostty terminal-object capture for exact split and focused-pane PDF export
- direct `recap detect` targeting via `--app`, `--title`, `--window-id`, `--active`, and `--active-window`
- live Codex transcript export for terminal-backed Zed windows
- `VISION.md` to document the product direction and release discipline

### Changed
- terminal windows now prefer text/scrollback export before screenshot fallback when a native path exists
- default output path now points to `~/Downloads`
- help and README now document the fastest exact Ghostty and Zed capture paths

### Fixed
- window discovery now preserves real multi-window apps that share one PID
- shell correlation now walks through intermediate parent processes like `login`
- PDF rendering for Codex transcripts now uses plain searchable text so large exports complete reliably

## v0.4.0

- direct PNG stitching
- median-cut quantization
- precise window targeting

## v0.3.1

- native cmux integration

## v0.3.0

- window detection
- interactive capture
- Ghostty pane discovery

## v0.2.0

- Claude log rendering
- `grab` capture flow

## v0.1.0

- initial PTY recording
- PDF and PNG exports
