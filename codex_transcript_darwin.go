//go:build darwin

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type processInfo struct {
	PID     int
	PPID    int
	TTY     string
	Command string
}

type windowShellCandidate struct {
	Shell ShellProc
	CWD   string
	Score int
}

type codexOpenRollout struct {
	FD   int
	Path string
}

type codexThreadMeta struct {
	ThreadID    string
	Source      string
	CreatedAt   int64
	UpdatedAt   int64
	RolloutPath string
}

type codexTranscript struct {
	RolloutPath string
	Title       string
	Data        []byte
}

type codexResponseEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexMessageItem struct {
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []codexContentBlock `json:"content"`
	Phase   string              `json:"phase"`
}

type codexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexFunctionCall struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Input     string `json:"input"`
}

type codexFunctionOutput struct {
	Type   string `json:"type"`
	Output string `json:"output"`
}

type codexWebSearchCall struct {
	Type   string               `json:"type"`
	Action codexWebSearchAction `json:"action"`
}

type codexWebSearchAction struct {
	Type    string   `json:"type"`
	Query   string   `json:"query"`
	Queries []string `json:"queries"`
}

type codexCustomToolOutput struct {
	Output string `json:"output"`
}

func captureWindowCodexTranscript(w WindowInfo) (*CaptureResult, error) {
	transcript, err := resolveWindowCodexTranscript(w)
	if err != nil {
		return nil, err
	}

	return &CaptureResult{
		Window:      w,
		ContentType: ContentTextPlain,
		Data:        transcript.Data,
		SearchText:  transcript.Data,
		Title:       transcript.Title,
	}, nil
}

func resolveWindowCodexTranscript(w WindowInfo) (*codexTranscript, error) {
	shells, err := listShellProcesses()
	if err != nil {
		return nil, fmt.Errorf("list shells: %w", err)
	}
	if len(shells) == 0 {
		return nil, fmt.Errorf("no shell processes found")
	}

	parentMap := listProcessParents()
	candidates := windowShellCandidates(w, shells, parentMap)
	shell, ok := pickWindowShellCandidate(candidates)
	if !ok {
		return nil, fmt.Errorf("no unique terminal session matched window %q", w.Name)
	}

	transcript, err := resolveCodexTranscriptForShell(shell.Shell.PID)
	if err != nil {
		return nil, err
	}

	if w.Name != "" {
		transcript.Title = fmt.Sprintf("codex — %s", w.Name)
	} else if shell.CWD != "" {
		transcript.Title = fmt.Sprintf("codex — %s", filepath.Base(shell.CWD))
	} else {
		transcript.Title = fmt.Sprintf("codex — %s", strings.ToLower(w.Owner))
	}

	return transcript, nil
}

func resolveCodexTranscriptForShell(shellPID int) (*codexTranscript, error) {
	processes, err := listProcesses()
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}

	descendants := descendantProcesses(shellPID, processes)
	if len(descendants) == 0 {
		return nil, fmt.Errorf("no child processes found for shell %d", shellPID)
	}

	var best *codexOpenRollout
	for _, proc := range descendants {
		if !looksLikeCodexProcess(proc.Command) {
			continue
		}
		openRollouts, err := openCodexRolloutPaths(proc.PID)
		if err != nil || len(openRollouts) == 0 {
			continue
		}
		candidate := pickBestCodexRollout(openRollouts, lookupCodexThreadMetadata(openRollouts))
		if candidate.Path == "" {
			continue
		}
		if best == nil || candidate.FD < best.FD {
			chosen := candidate
			best = &chosen
		}
	}

	if best == nil {
		return nil, fmt.Errorf("no active Codex rollout found under shell %d", shellPID)
	}

	data, err := os.ReadFile(best.Path)
	if err != nil {
		return nil, fmt.Errorf("read Codex rollout %s: %w", best.Path, err)
	}

	return &codexTranscript{
		RolloutPath: best.Path,
		Data:        renderCodexConversation(data),
	}, nil
}

func listProcesses() ([]processInfo, error) {
	out, err := exec.Command("ps", "-ax", "-o", "pid=,ppid=,tty=,command=").Output()
	if err != nil {
		return nil, err
	}

	var result []processInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}

		result = append(result, processInfo{
			PID:     pid,
			PPID:    ppid,
			TTY:     fields[2],
			Command: strings.Join(fields[3:], " "),
		})
	}

	return result, nil
}

func descendantProcesses(rootPID int, processes []processInfo) []processInfo {
	children := make(map[int][]processInfo)
	for _, proc := range processes {
		children[proc.PPID] = append(children[proc.PPID], proc)
	}

	var result []processInfo
	queue := []int{rootPID}
	seen := map[int]bool{rootPID: true}

	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]

		for _, child := range children[pid] {
			if seen[child.PID] {
				continue
			}
			seen[child.PID] = true
			result = append(result, child)
			queue = append(queue, child.PID)
		}
	}

	return result
}

func looksLikeCodexProcess(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "/codex/codex") ||
		strings.Contains(lower, "/codex --") ||
		strings.HasPrefix(lower, "codex ") ||
		strings.Contains(lower, " codex ")
}

func shellsForAppPID(targetPID int, shells []ShellProc, parentMap map[int]int) []ShellProc {
	var matched []ShellProc
	for _, shell := range shells {
		pid := shell.PPID
		for hops := 0; hops < 40 && pid > 1; hops++ {
			if pid == targetPID {
				matched = append(matched, shell)
				break
			}
			parent, ok := parentMap[pid]
			if !ok || parent <= 0 || parent == pid {
				break
			}
			pid = parent
		}
	}
	return matched
}

func windowShellCandidates(w WindowInfo, shells []ShellProc, parentMap map[int]int) []windowShellCandidate {
	var candidates []windowShellCandidate
	for _, shell := range shellsForAppPID(w.PID, shells, parentMap) {
		cwd := processWorkingDir(shell.PID)
		candidates = append(candidates, windowShellCandidate{
			Shell: shell,
			CWD:   cwd,
			Score: scoreWindowToCWD(w.Name, cwd),
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Shell.PID < candidates[j].Shell.PID
	})
	return candidates
}

func pickWindowShellCandidate(candidates []windowShellCandidate) (windowShellCandidate, bool) {
	if len(candidates) == 0 {
		return windowShellCandidate{}, false
	}
	ordered := append([]windowShellCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score != ordered[j].Score {
			return ordered[i].Score > ordered[j].Score
		}
		return ordered[i].Shell.PID < ordered[j].Shell.PID
	})
	if len(ordered) == 1 {
		return ordered[0], true
	}
	if ordered[0].Score > 0 && ordered[0].Score > ordered[1].Score {
		return ordered[0], true
	}
	return windowShellCandidate{}, false
}

func scoreWindowToCWD(windowName, cwd string) int {
	windowName = normalizeWindowToken(windowName)
	base := normalizeWindowToken(filepath.Base(cwd))
	cwd = normalizeWindowToken(cwd)
	if windowName == "" || cwd == "" {
		return 0
	}

	switch {
	case base != "" && windowName == base:
		return 100
	case base != "" && (strings.Contains(windowName, base) || strings.Contains(base, windowName)):
		return 80
	case strings.Contains(cwd, windowName):
		return 60
	default:
		return sharedTokenScore(windowName, base) * 10
	}
}

func normalizeWindowToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func sharedTokenScore(a, b string) int {
	if a == "" || b == "" {
		return 0
	}

	set := make(map[string]bool)
	for _, token := range strings.Fields(a) {
		set[token] = true
	}

	score := 0
	for _, token := range strings.Fields(b) {
		if set[token] {
			score++
		}
	}
	return score
}

func processWorkingDir(pid int) string {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimSpace(strings.TrimPrefix(line, "n"))
		}
	}
	return ""
}

func openCodexRolloutPaths(pid int) ([]codexOpenRollout, error) {
	out, err := exec.Command("lsof", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil {
		return nil, err
	}

	var result []codexOpenRollout
	currentFD := -1
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "f") {
			currentFD = parseLeadingInt(strings.TrimPrefix(line, "f"))
			continue
		}
		if !strings.HasPrefix(line, "n") || currentFD < 0 {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "n"))
		if strings.Contains(path, "/.codex/sessions/") && strings.HasSuffix(path, ".jsonl") {
			result = append(result, codexOpenRollout{FD: currentFD, Path: path})
		}
	}
	return result, nil
}

func parseLeadingInt(s string) int {
	var digits strings.Builder
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		digits.WriteRune(r)
	}
	if digits.Len() == 0 {
		return -1
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil {
		return -1
	}
	return n
}

func lookupCodexThreadMetadata(paths []codexOpenRollout) []codexThreadMeta {
	if len(paths) == 0 {
		return nil
	}

	var quoted []string
	for _, path := range paths {
		quoted = append(quoted, quoteSQLiteString(path.Path))
	}

	query := fmt.Sprintf(
		"SELECT id, source, created_at, updated_at, rollout_path FROM threads WHERE rollout_path IN (%s);",
		strings.Join(quoted, ","),
	)

	out, err := exec.Command(
		"sqlite3",
		"-separator", "\t",
		filepath.Join(userHomeDir(), ".codex", "state_5.sqlite"),
		query,
	).Output()
	if err != nil {
		return nil
	}

	var metas []codexThreadMeta
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			continue
		}
		createdAt, _ := strconv.ParseInt(parts[2], 10, 64)
		updatedAt, _ := strconv.ParseInt(parts[3], 10, 64)
		metas = append(metas, codexThreadMeta{
			ThreadID:    parts[0],
			Source:      parts[1],
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			RolloutPath: parts[4],
		})
	}
	return metas
}

func pickBestCodexRollout(candidates []codexOpenRollout, metas []codexThreadMeta) codexOpenRollout {
	if len(candidates) == 0 {
		return codexOpenRollout{}
	}

	metaByPath := make(map[string]codexThreadMeta, len(metas))
	for _, meta := range metas {
		metaByPath[meta.RolloutPath] = meta
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a := metaByPath[candidates[i].Path]
		b := metaByPath[candidates[j].Path]
		scoreA := codexSourceScore(a.Source)
		scoreB := codexSourceScore(b.Source)
		if scoreA != scoreB {
			return scoreA > scoreB
		}
		if a.CreatedAt != 0 && b.CreatedAt != 0 && a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return candidates[i].FD < candidates[j].FD
	})

	return candidates[0]
}

func codexSourceScore(source string) int {
	switch {
	case source == "cli":
		return 100
	case strings.Contains(source, `"parent_thread_id"`):
		return 10
	case source != "":
		return 1
	default:
		return 0
	}
}

func quoteSQLiteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func userHomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func renderCodexConversation(jsonlData []byte) []byte {
	var out []byte
	w := func(s string) { out = append(out, []byte(s+"\n")...) }

	const divider = "──────────────────────────────────────────────────────────────────────────────"

	scanner := bufio.NewScanner(bytes.NewReader(jsonlData))
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 64*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var envelope codexResponseEnvelope
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if envelope.Type != "response_item" {
			continue
		}

		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(envelope.Payload, &header); err != nil {
			continue
		}

		switch header.Type {
		case "message":
			var msg codexMessageItem
			if err := json.Unmarshal(envelope.Payload, &msg); err != nil {
				continue
			}
			text := extractCodexMessageText(msg.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}

			switch msg.Role {
			case "developer":
				continue
			case "user":
				if shouldSkipCodexUserText(text) {
					continue
				}
				w("")
				w(divider)
				w("You")
				w(divider)
				w("")
				for _, wl := range wordWrap(text, 90) {
					w("  " + wl)
				}
			case "assistant":
				label := "◆ Codex"
				if msg.Phase == "commentary" {
					label = "◆ Codex Update"
				}
				w("")
				w(divider)
				w(label)
				w(divider)
				w("")
				renderPlainMarkdownText(text, w)
			}

		case "function_call":
			var call codexFunctionCall
			if err := json.Unmarshal(envelope.Payload, &call); err != nil {
				continue
			}
			w("")
			w(fmt.Sprintf("  %s %s", codexToolBadge(call.Name), summarizeCodexToolCall(call.Name, call.Arguments)))

		case "function_call_output":
			var output codexFunctionOutput
			if err := json.Unmarshal(envelope.Payload, &output); err != nil {
				continue
			}
			renderCodexToolOutput(output.Output, w)

		case "custom_tool_call":
			var call codexFunctionCall
			if err := json.Unmarshal(envelope.Payload, &call); err != nil {
				continue
			}
			w("")
			w(fmt.Sprintf("  %s %s", codexToolBadge(call.Name), summarizeCodexCustomToolCall(call.Name, call.Input)))

		case "custom_tool_call_output":
			var output codexFunctionOutput
			if err := json.Unmarshal(envelope.Payload, &output); err != nil {
				continue
			}
			renderCodexToolOutput(extractCodexCustomToolOutput(output.Output), w)

		case "web_search_call":
			var searchCall codexWebSearchCall
			if err := json.Unmarshal(envelope.Payload, &searchCall); err != nil {
				continue
			}
			query := searchCall.Action.Query
			if query == "" && len(searchCall.Action.Queries) > 0 {
				query = searchCall.Action.Queries[0]
			}
			if query != "" {
				w("")
				w(fmt.Sprintf("  %s web search: %s", codexToolBadge("web_search"), query))
			}
		}
	}

	w("")
	w(divider)
	w("End of Codex transcript")

	return out
}

func extractCodexMessageText(blocks []codexContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if strings.TrimSpace(block.Text) == "" {
			continue
		}
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n")
}

func shouldSkipCodexUserText(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<environment_context>") ||
		strings.HasPrefix(trimmed, "<skill>") ||
		strings.HasPrefix(trimmed, "<turn_aborted>")
}

func codexToolBadge(name string) string {
	switch name {
	case "exec_command":
		return "[$]"
	case "write_stdin":
		return "[>]"
	case "view_image":
		return "[img]"
	case "apply_patch":
		return "[patch]"
	case "web_search":
		return "[web]"
	default:
		return "[tool]"
	}
}

func summarizeCodexToolCall(name, args string) string {
	switch name {
	case "exec_command":
		var payload struct {
			Cmd string `json:"cmd"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil && payload.Cmd != "" {
			return payload.Cmd
		}
	case "write_stdin":
		var payload struct {
			SessionID int    `json:"session_id"`
			Chars     string `json:"chars"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil {
			if payload.Chars == "" {
				return fmt.Sprintf("poll session %d", payload.SessionID)
			}
			return fmt.Sprintf("write to session %d", payload.SessionID)
		}
	case "view_image":
		var payload struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &payload) == nil && payload.Path != "" {
			return payload.Path
		}
	}
	if len(args) > 160 {
		return args[:157] + "..."
	}
	return fmt.Sprintf("%s %s", name, strings.TrimSpace(args))
}

func summarizeCodexCustomToolCall(name, input string) string {
	if name == "apply_patch" {
		for _, line := range strings.Split(input, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "*** Update File: ") ||
				strings.HasPrefix(line, "*** Add File: ") ||
				strings.HasPrefix(line, "*** Delete File: ") {
				return strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(line, "*** Update File: "), "*** Add File: "), "*** Delete File: ")
			}
		}
	}
	if len(input) > 160 {
		return input[:157] + "..."
	}
	return fmt.Sprintf("%s", name)
}

func renderPlainMarkdownText(text string, w func(string)) {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			w("")
			continue
		}
		for _, wrapped := range wordWrap(line, 90) {
			w("  " + wrapped)
		}
	}
}

func renderCodexToolOutput(output string, w func(string)) {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return
	}
	w("")
	w("  output:")
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			w("  |")
			continue
		}
		w("  | " + line)
	}
}

func extractCodexCustomToolOutput(raw string) string {
	var payload codexCustomToolOutput
	if json.Unmarshal([]byte(raw), &payload) == nil && payload.Output != "" {
		return payload.Output
	}
	return raw
}
