package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Session struct {
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitempty"`
	CWD   string    `json:"cwd"`
	Shell string    `json:"shell"`
	Cols  int       `json:"cols"`
	Rows  int       `json:"rows"`
}

func recapDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".recap")
}

func sessionsDir() string {
	return filepath.Join(recapDir(), "sessions")
}

func NewSession() *Session {
	now := time.Now()
	cwd, _ := os.Getwd()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}

	return &Session{
		ID:    now.Format("2006-01-02_15-04-05"),
		Start: now,
		CWD:   cwd,
		Shell: shell,
	}
}

func (s *Session) Dir() string {
	return filepath.Join(sessionsDir(), s.ID)
}

func (s *Session) OutputPath() string {
	return filepath.Join(s.Dir(), "output.log")
}

func (s *Session) MetaPath() string {
	return filepath.Join(s.Dir(), "meta.json")
}

func (s *Session) EnsureDir() error {
	return os.MkdirAll(s.Dir(), 0755)
}

func (s *Session) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.MetaPath(), data, 0644)
}

func (s *Session) Duration() time.Duration {
	if s.End.IsZero() {
		return time.Since(s.Start)
	}
	return s.End.Sub(s.Start)
}

func LoadSession(id string) (*Session, error) {
	metaPath := filepath.Join(sessionsDir(), id, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("session %q not found", id)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func ListSessions() ([]*Session, error) {
	dir := sessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []*Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := LoadSession(e.Name())
		if err != nil {
			continue
		}
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Start.After(sessions[j].Start)
	})

	return sessions, nil
}

func LatestSession() (*Session, error) {
	sessions, err := ListSessions()
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions found — run 'recap' to start recording")
	}
	return sessions[0], nil
}
