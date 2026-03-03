package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Claude Code JSONL conversation structures
type ccMessage struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Message json.RawMessage `json:"message"`
}

type ccInnerMessage struct {
	Content json.RawMessage `json:"content"`
}

type ccContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Name     string `json:"name"`     // tool_use
	Thinking string `json:"thinking"` // thinking
	Input    json.RawMessage `json:"input"` // tool_use input
	Content  json.RawMessage `json:"content"` // tool_result content
}

type ccToolResult struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"content"`
}

func cmdClaude() {
	format := "pdf"
	if hasFlag("--png") {
		format = "png"
	}
	outputPath := getFlag("--output")
	if outputPath == "" {
		outputPath = getFlag("-o")
	}
	sessionFile := getFlag("--session")

	// Find the JSONL file
	var jsonlPath string
	if sessionFile != "" {
		jsonlPath = sessionFile
	} else {
		// Auto-detect: find the most recent JSONL in Claude's project dir
		var err error
		jsonlPath, err = findLatestClaudeSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Reading Claude session: %s\n", filepath.Base(jsonlPath))

	// Parse the conversation
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	ansiOutput := renderClaudeConversation(data)

	sess := &Session{
		ID:    time.Now().Format("2006-01-02_15-04-05"),
		Start: time.Now(),
		End:   time.Now(),
		CWD:   "claude-code",
		Shell: "claude",
		Cols:  120,
		Rows:  50,
	}

	if outputPath == "" {
		home, _ := os.UserHomeDir()
		outputPath = filepath.Join(home, "Downloads",
			fmt.Sprintf("recap-claude-%s.%s", sess.ID, format))
	}

	fmt.Fprintf(os.Stderr, "\033[90m[recap]\033[0m Rendering %s...\n", format)

	if format == "png" {
		err = RenderSessionPNG(sess, outputPath, ansiOutput)
	} else {
		err = RenderSessionPDF(sess, outputPath, ansiOutput)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31merror:\033[0m %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "\033[32m✓\033[0m %s\n", outputPath)
	openFile(outputPath)
}

func findLatestClaudeSession() (string, error) {
	home, _ := os.UserHomeDir()
	projectsDir := filepath.Join(home, ".claude", "projects")

	// Find all project dirs
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", fmt.Errorf("no Claude sessions found at %s", projectsDir)
	}

	// Collect all JSONL files across all projects
	type jsonlFile struct {
		path    string
		modTime time.Time
	}
	var files []jsonlFile

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projDir := filepath.Join(projectsDir, e.Name())
		jsonls, _ := filepath.Glob(filepath.Join(projDir, "*.jsonl"))
		for _, f := range jsonls {
			info, err := os.Stat(f)
			if err == nil {
				files = append(files, jsonlFile{path: f, modTime: info.ModTime()})
			}
		}
	}

	if len(files) == 0 {
		return "", fmt.Errorf("no Claude sessions found")
	}

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	return files[0].path, nil
}

func renderClaudeConversation(jsonlData []byte) []byte {
	var out []byte
	w := func(s string) { out = append(out, []byte(s+"\n")...) }

	// ANSI styling constants
	dim := "\033[90m"
	reset := "\033[0m"
	bold := "\033[1m"
	_ = "\033[34m"  // blue
	green := "\033[32m"
	yellow := "\033[33m"
	cyan := "\033[36m"
	_ = "\033[35m"  // magenta
	white := "\033[97m"
	boldBlue := "\033[1;34m"
	boldMagenta := "\033[1;35m"
	bgGreen := "\033[42;30m"
	bgYellow := "\033[43;30m"
	bgCyan := "\033[46;30m"
	bgRed := "\033[41;37m"

	lines := strings.Split(string(jsonlData), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg ccMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "user":
			// Parse user message content
			var inner ccInnerMessage
			if err := json.Unmarshal(msg.Message, &inner); err != nil {
				continue
			}

			// Content can be a string or array
			var text string
			var rawStr string
			if err := json.Unmarshal(inner.Content, &rawStr); err == nil {
				text = rawStr
			} else {
				var blocks []ccContentBlock
				if err := json.Unmarshal(inner.Content, &blocks); err == nil {
					for _, b := range blocks {
						if b.Type == "text" {
							text = b.Text
							break
						}
						if b.Type == "tool_result" {
							// Tool results in user messages (Claude's tool_result protocol)
							var resultContent string
							if err := json.Unmarshal(b.Content, &resultContent); err == nil {
								text = resultContent
							} else {
								var resultBlocks []struct {
									Type string `json:"type"`
									Text string `json:"text"`
								}
								if err := json.Unmarshal(b.Content, &resultBlocks); err == nil {
									for _, rb := range resultBlocks {
										if rb.Type == "text" {
											text = rb.Text
											break
										}
									}
								}
							}
						}
					}
				}
			}

			if text != "" && !strings.HasPrefix(text, "<") {
				w("")
				w(fmt.Sprintf("%s━━━ %s⟩ You%s %s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s", dim, boldBlue, reset, dim, reset))
				w("")
				// Word-wrap user text
				for _, wl := range wordWrap(text, 90) {
					w(fmt.Sprintf("  %s%s%s", white, wl, reset))
				}
			}

		case "assistant":
			var inner ccInnerMessage
			if err := json.Unmarshal(msg.Message, &inner); err != nil {
				continue
			}

			var blocks []ccContentBlock
			if err := json.Unmarshal(inner.Content, &blocks); err != nil {
				continue
			}

			hasText := false
			for _, b := range blocks {
				switch b.Type {
				case "thinking":
					// Skip thinking blocks — they're internal
					continue

				case "text":
					if !hasText {
						w("")
						w(fmt.Sprintf("%s━━━ %s◆ Claude%s %s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s", dim, boldMagenta, reset, dim, reset))
						w("")
						hasText = true
					}
					renderMarkdownText(b.Text, w, dim, reset, bold, cyan, green, yellow)

				case "tool_use":
					toolLabel := toolBadge(b.Name, bgGreen, bgYellow, bgCyan, bgRed, reset)
					inputStr := summarizeToolInput(b.Name, b.Input)
					w(fmt.Sprintf("  %s %s%s%s", toolLabel, dim, inputStr, reset))

				case "tool_result":
					// Tool results are in user messages, skip here
					continue
				}
			}
		}
	}

	w("")
	w(fmt.Sprintf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s", dim, reset))
	w(fmt.Sprintf("  %sEnd of conversation%s", dim, reset))

	return out
}

func toolBadge(name string, bgGreen, bgYellow, bgCyan, bgRed, reset string) string {
	switch name {
	case "Bash":
		return fmt.Sprintf("%s $ %s", bgGreen, reset)
	case "Write":
		return fmt.Sprintf("%s W %s", bgYellow, reset)
	case "Edit":
		return fmt.Sprintf("%s E %s", bgYellow, reset)
	case "Read":
		return fmt.Sprintf("%s R %s", bgCyan, reset)
	case "Glob":
		return fmt.Sprintf("%s G %s", bgCyan, reset)
	case "Grep":
		return fmt.Sprintf("%s ? %s", bgCyan, reset)
	case "Task":
		return fmt.Sprintf("%s T %s", bgRed, reset)
	default:
		return fmt.Sprintf("%s ⚡%s", bgCyan, reset)
	}
}

func summarizeToolInput(name string, raw json.RawMessage) string {
	var input map[string]interface{}
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}

	switch name {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			// Truncate long commands
			if len(cmd) > 80 {
				cmd = cmd[:77] + "..."
			}
			return cmd
		}
	case "Write":
		if fp, ok := input["file_path"].(string); ok {
			return filepath.Base(fp)
		}
	case "Edit":
		if fp, ok := input["file_path"].(string); ok {
			return filepath.Base(fp)
		}
	case "Read":
		if fp, ok := input["file_path"].(string); ok {
			return filepath.Base(fp)
		}
	case "Glob":
		if p, ok := input["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := input["pattern"].(string); ok {
			return p
		}
	case "Task":
		if d, ok := input["description"].(string); ok {
			return d
		}
	}
	return name
}

func renderMarkdownText(text string, w func(string), dim, reset, bold, cyan, green, yellow string) {
	lines := strings.Split(text, "\n")
	inCodeBlock := false

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				lang := strings.TrimPrefix(line, "```")
				w(fmt.Sprintf("  %s┌─── %s%s%s", dim, lang, reset+dim, reset))
			} else {
				w(fmt.Sprintf("  %s└───%s", dim, reset))
			}
			continue
		}

		if inCodeBlock {
			w(fmt.Sprintf("  %s│%s %s%s%s", dim, reset, cyan, line, reset))
			continue
		}

		// Headers
		if strings.HasPrefix(line, "## ") {
			w(fmt.Sprintf("  %s%s%s", bold, strings.TrimPrefix(line, "## "), reset))
			continue
		}
		if strings.HasPrefix(line, "# ") {
			w(fmt.Sprintf("  %s%s%s", bold, strings.TrimPrefix(line, "# "), reset))
			continue
		}

		// Bold text inline
		rendered := line
		// Simple bold: **text**
		for strings.Contains(rendered, "**") {
			start := strings.Index(rendered, "**")
			end := strings.Index(rendered[start+2:], "**")
			if end == -1 {
				break
			}
			end += start + 2
			inner := rendered[start+2 : end]
			rendered = rendered[:start] + bold + inner + reset + rendered[end+2:]
		}

		// Inline code: `text`
		for strings.Contains(rendered, "`") {
			start := strings.Index(rendered, "`")
			end := strings.Index(rendered[start+1:], "`")
			if end == -1 {
				break
			}
			end += start + 1
			inner := rendered[start+1 : end]
			rendered = rendered[:start] + cyan + inner + reset + rendered[end+1:]
		}

		// Bullet points
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			indent := len(line) - len(strings.TrimLeft(line, " "))
			padding := strings.Repeat(" ", indent)
			content := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			w(fmt.Sprintf("  %s%s• %s%s", padding, green, content, reset))
			continue
		}

		// Table rows
		if strings.Contains(line, "|") && strings.Count(line, "|") >= 2 {
			w(fmt.Sprintf("  %s%s%s", dim, line, reset))
			continue
		}

		// Regular text
		if strings.TrimSpace(rendered) == "" {
			w("")
		} else {
			w(fmt.Sprintf("  %s", rendered))
		}
	}
}

func wordWrap(text string, width int) []string {
	if len(text) <= width {
		return []string{text}
	}

	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			lines = append(lines, "")
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		current := words[0]
		for _, word := range words[1:] {
			if len(current)+1+len(word) > width {
				lines = append(lines, current)
				current = word
			} else {
				current += " " + word
			}
		}
		lines = append(lines, current)
	}
	return lines
}
