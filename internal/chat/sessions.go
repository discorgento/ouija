package chat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/atomicfile"
)

// ─── Sessions (ouija chat --continue, /sessions) ──────────────────────

// sessionsPickerLabel is the picker's label while it lists sessions.
const sessionsPickerLabel = "Sessions"

// maxSessions is how many sessions a project keeps; the oldest are removed.
const maxSessions = 30

// session is a saved conversation: the history the model sees and the
// transcript the developer saw. It holds the files the tools read, so it is
// written 0600 in the user's cache directory, never in the project.
type session struct {
	ID       string           `json:"id"`
	Root     string           `json:"root"`
	Title    string           `json:"title"`
	Updated  time.Time        `json:"updated"`
	Model    string           `json:"model"`
	Provider string           `json:"provider"`
	Messages int              `json:"messages"`
	History  []engine.Message `json:"history"`
	Lines    []savedLine      `json:"lines"`
}

// savedLine is a transcript line as saved (its render cache left out).
type savedLine struct {
	Role  string             `json:"role"`
	Text  string             `json:"text,omitempty"`
	TS    string             `json:"ts,omitempty"`
	Meta  string             `json:"meta,omitempty"`
	Path  string             `json:"path,omitempty"`
	Hint  string             `json:"hint,omitempty"`
	Trace string             `json:"trace,omitempty"`
	Tools engine.TurnSummary `json:"tools,omitzero"`
}

// sessionDir is where root's sessions are kept: one directory per project
// under the user cache directory.
func sessionDir(root string) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(cache, "ouija", "sessions", hex.EncodeToString(sum[:8])), nil
}

// saveSession writes the conversation so far (nothing before the first
// message), then trims the project's oldest sessions.
func (m *model) saveSession() error {
	if m.msgCount == 0 {
		return nil
	}
	cfg := m.eng.Config()
	dir, err := sessionDir(cfg.ProjectRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if m.sessionID == "" {
		m.sessionID = time.Now().Format("20060102-150405")
	}
	s := session{
		ID: m.sessionID, Root: cfg.ProjectRoot, Updated: time.Now(),
		Model: cfg.Model, Provider: cfg.Provider, Messages: m.msgCount, History: m.history,
	}
	for _, l := range m.lines {
		if s.Title == "" && l.role == "user" {
			s.Title = l.text
		}
		if l.role == "help" {
			continue // redrawn on demand, not worth keeping
		}
		s.Lines = append(s.Lines, savedLine{Role: l.role, Text: l.text, TS: l.ts, Meta: l.meta, Path: l.path, Hint: l.hint, Trace: l.trace, Tools: l.tools})
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(filepath.Join(dir, m.sessionID+".json"), b, 0o600, false); err != nil {
		return err
	}
	if list, _ := listSessions(cfg.ProjectRoot); len(list) > maxSessions {
		for _, old := range list[maxSessions:] {
			os.Remove(filepath.Join(dir, old.ID+".json"))
		}
	}
	return nil
}

// listSessions returns root's sessions, the latest first.
func listSessions(root string) ([]session, error) {
	dir, err := sessionDir(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []session
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := readSession(filepath.Join(dir, e.Name()))
		if err != nil || s.Root != root {
			continue
		}
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b session) int { return b.Updated.Compare(a.Updated) })
	return out, nil
}

func readSession(path string) (*session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s session
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// restoreSession puts a saved conversation back: the model sees its history,
// the transcript shows its lines, and later turns save into it.
func (m *model) restoreSession(s *session) {
	m.history = s.History
	m.lines = m.lines[:0]
	for _, l := range s.Lines {
		m.lines = append(m.lines, line{role: l.Role, text: l.Text, ts: l.TS, meta: l.Meta, path: l.Path, hint: l.Hint, trace: l.Trace, tools: l.Tools})
	}
	m.msgCount = s.Messages
	m.sessionID = s.ID
	m.seenLines = len(m.lines)
	m.lines = append(m.lines, line{role: "banner", text: fmt.Sprintf("✓ resumed the conversation of %s (%d messages)", s.Updated.Format("2 Jan 15:04"), s.Messages)})
	m.autoScroll = true
	m.rebuildContent()
}

// continueLatest restores the project's latest session (ouija chat
// --continue); it says so when there is none.
func (m *model) continueLatest() {
	list, _ := listSessions(m.eng.Config().ProjectRoot)
	if len(list) == 0 {
		m.lines = append(m.lines, line{role: "banner", text: "no earlier conversation in this project — starting a new one"})
		m.rebuildContent()
		return
	}
	m.restoreSession(&list[0])
}

// showSessionsPicker lists the project's sessions to resume one (/sessions).
func (m *model) showSessionsPicker() bool {
	list, _ := listSessions(m.eng.Config().ProjectRoot)
	if len(list) == 0 {
		return false
	}
	items := make([]string, len(list))
	values := make([]string, len(list))
	for i, s := range list {
		title := strings.Join(strings.Fields(s.Title), " ")
		if len(title) > 60 {
			title = strings.ToValidUTF8(title[:57], "") + "…"
		}
		items[i] = fmt.Sprintf("%s · %d msg · %s", s.Updated.Format("2 Jan 15:04"), s.Messages, title)
		values[i] = s.ID
	}
	m.picker.Show(items, values, sessionsPickerLabel)
	m.picker.Current = m.sessionID
	return true
}

// resumeSession loads the session id of this project.
func (m *model) resumeSession(id string) {
	dir, err := sessionDir(m.eng.Config().ProjectRoot)
	if err == nil {
		var s *session
		if s, err = readSession(filepath.Join(dir, id+".json")); err == nil {
			m.restoreSession(s)
			return
		}
	}
	m.addBanner("⚠ could not resume: " + err.Error())
}
