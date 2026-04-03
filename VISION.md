# recap — Vision

## mission

make terminal capture precise enough to trust, headless enough to automate, and polished enough to hand directly to another person as a PDF.

## product direction

`recap` should feel less like a screenshot toy and more like a terminal export engine:

- content first when possible
- pixels only when necessary
- exact targeting instead of "probably the right window"
- app-native adapters instead of one generic capture path
- pdf-first outputs that are searchable, shareable, and office-ready

## what "better" means for recap

### 1. exact selection

users should be able to name the thing they want:

- a Ghostty split
- a focused pane
- a Zed window by title
- a concrete macOS window ID
- a tmux pane
- a cmux surface

the tool should fail clearly when a selector is ambiguous, not silently capture the wrong thing.

### 2. headless capture

the best capture path should not depend on:

- taking over the cursor
- moving focus to the app
- sending visible keypresses
- requiring the user to stage the window manually

Ghostty terminal objects, Codex rollout logs, tmux panes, cmux surfaces, and similar native data sources are the preferred direction.

### 3. searchable pdfs by default

the default artifact should be:

- easy to skim
- printable
- searchable
- suitable for design reviews, debugging notes, status updates, and handoff docs

PNG remains important for pixel-fidelity cases, but the center of gravity is document-ready PDF export.

### 4. adapters over heuristics

the long-term moat is a growing library of capture adapters:

- Ghostty
- Codex-backed editor terminals
- tmux
- cmux
- Claude logs
- future editor or terminal APIs

heuristics still matter, but they should only bridge gaps until a native path exists.

## near-term roadmap

- richer Ghostty pane identity and selection beyond current title/cwd matching
- more editor-native terminal adapters beyond Codex in Zed
- stronger window-picker UX without sacrificing headless automation
- better transcript compaction for very long interactive sessions
- export metadata and filenames that communicate the source more clearly

## long-term roadmap

- screen capture kit helper for exact pixel capture without user interaction
- app-specific transcript and scrollback providers for more terminals/editors
- capture jobs that can run unattended from scripts or scheduled workflows
- stronger changelog/release discipline so shipped improvements are visible immediately

## release discipline

`recap` should ship with:

- semantic version tags
- focused conventional commits
- a readable changelog
- a progress log that explains what materially improved

the product should be as legible in the repo as it is in the rendered output.
