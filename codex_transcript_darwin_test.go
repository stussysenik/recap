//go:build darwin

package main

import (
	"strings"
	"testing"
)

func TestPickWindowShellCandidatePrefersExactCWDMatch(t *testing.T) {
	candidates := []windowShellCandidate{
		{Shell: ShellProc{PID: 10}, CWD: "/Users/s3nik/Desktop/v0-ipod", Score: scoreWindowToCWD("expense-os", "/Users/s3nik/Desktop/v0-ipod")},
		{Shell: ShellProc{PID: 20}, CWD: "/Users/s3nik/Desktop/expense-os", Score: scoreWindowToCWD("expense-os", "/Users/s3nik/Desktop/expense-os")},
	}

	chosen, ok := pickWindowShellCandidate(candidates)
	if !ok {
		t.Fatal("expected a shell candidate")
	}
	if chosen.Shell.PID != 20 {
		t.Fatalf("picked PID %d, want 20", chosen.Shell.PID)
	}
}

func TestPickBestCodexRolloutPrefersCLIThread(t *testing.T) {
	candidates := []codexOpenRollout{
		{FD: 66, Path: "/tmp/subagent.jsonl"},
		{FD: 22, Path: "/tmp/cli.jsonl"},
	}
	metas := []codexThreadMeta{
		{Source: `{"subagent":{"thread_spawn":{"parent_thread_id":"abc"}}}`, RolloutPath: "/tmp/subagent.jsonl"},
		{Source: "cli", RolloutPath: "/tmp/cli.jsonl"},
	}

	chosen := pickBestCodexRollout(candidates, metas)
	if chosen.Path != "/tmp/cli.jsonl" {
		t.Fatalf("picked %q, want cli rollout", chosen.Path)
	}
}

func TestRenderCodexConversationSkipsReasoningAndDeveloperMessages(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"secret developer prompt"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"show me the terminal output"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"I am checking the repo."}]}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"git status --short\"}"}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","output":"Command: /bin/zsh -lc 'git status --short'\nOutput:\n M README.md\n"}}`,
		`{"type":"response_item","payload":{"type":"reasoning","encrypted_content":"hidden"}}`,
	}, "\n")

	rendered := string(renderCodexConversation([]byte(raw)))
	if strings.Contains(rendered, "secret developer prompt") {
		t.Fatal("developer prompt should be skipped")
	}
	if strings.Contains(rendered, "encrypted_content") {
		t.Fatal("reasoning should be skipped")
	}
	if strings.Contains(rendered, "\x1b[") {
		t.Fatal("rendered transcript should be plain text")
	}
	if !strings.Contains(rendered, "show me the terminal output") {
		t.Fatal("user message missing")
	}
	if !strings.Contains(rendered, "I am checking the repo.") {
		t.Fatal("assistant message missing")
	}
	if !strings.Contains(rendered, "git status --short") {
		t.Fatal("tool call summary missing")
	}
	if !strings.Contains(rendered, "M README.md") {
		t.Fatal("tool output missing")
	}
}
