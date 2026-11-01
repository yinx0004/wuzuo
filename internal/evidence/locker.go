// SPDX-License-Identifier: Apache-2.0

// Package evidence implements the evidence locker: every tool output observed
// during an investigation is numbered (E1, E2, ...), redacted, sealed with a
// SHA-256 digest and persisted, so conclusions can cite it and the case can
// be re-examined later.
package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yinx0004/wuzuo/pkg/redact"
)

// Evidence is one sealed observation.
type Evidence struct {
	ID          string          `json:"id"`
	Tool        string          `json:"tool"`
	Target      string          `json:"target,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	CollectedAt time.Time       `json:"collected_at"`
	DurationMS  int64           `json:"duration_ms"`
	// Content is the full (redacted) output.
	Content string `json:"content"`
	// Error is set when the probe itself failed; failures are evidence too.
	Error  string `json:"error,omitempty"`
	SHA256 string `json:"sha256"`
}

// Excerpt returns the content as shown to the model, capped at max characters
// with the head and tail kept.
func (e *Evidence) Excerpt(max int) string {
	body := e.Content
	if e.Error != "" {
		body = "PROBE FAILED: " + e.Error
	}
	r := []rune(body)
	if max <= 0 || len(r) <= max {
		return body
	}
	head := max * 3 / 4
	tail := max - head
	return fmt.Sprintf("%s\n…[%d characters omitted; full content sealed as %s]…\n%s",
		string(r[:head]), len(r)-max, e.ID, string(r[len(r)-tail:]))
}

// Locker stores evidence for one case.
type Locker struct {
	mu     sync.Mutex
	dir    string // empty: in-memory only
	redact bool
	items  []*Evidence
	byID   map[string]*Evidence
}

// NewLocker creates a locker persisting to dir (if non-empty).
func NewLocker(dir string, redactSecrets bool) (*Locker, error) {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	return &Locker{dir: dir, redact: redactSecrets, byID: map[string]*Evidence{}}, nil
}

// Load restores a locker from a case directory.
func Load(dir string) (*Locker, error) {
	l := &Locker{dir: dir, byID: map[string]*Evidence{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, ent.Name())) //nolint:gosec // files listed from the case directory
		if err != nil {
			return nil, err
		}
		var e Evidence
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("%s: %w", ent.Name(), err)
		}
		if e.SHA256 != digest(&e) {
			return nil, fmt.Errorf("evidence %s failed integrity check", e.ID)
		}
		l.items = append(l.items, &e)
		l.byID[e.ID] = &e
	}
	sort.Slice(l.items, func(i, j int) bool { return idNum(l.items[i].ID) < idNum(l.items[j].ID) })
	return l, nil
}

// Record seals a new evidence item and returns it.
func (l *Locker) Record(tool, target string, args json.RawMessage, content string, probeErr error, started time.Time) (*Evidence, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := &Evidence{
		ID:          "E" + strconv.Itoa(len(l.items)+1),
		Tool:        tool,
		Target:      target,
		Args:        args,
		CollectedAt: started.UTC(),
		DurationMS:  time.Since(started).Milliseconds(),
		Content:     content,
	}
	if probeErr != nil {
		e.Error = probeErr.Error()
	}
	if l.redact {
		e.Content = redact.Secrets(e.Content)
		e.Error = redact.Secrets(e.Error)
	}
	e.SHA256 = digest(e)
	l.items = append(l.items, e)
	l.byID[e.ID] = e
	if l.dir != "" {
		data, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			return e, err
		}
		if err := os.WriteFile(filepath.Join(l.dir, e.ID+".json"), data, 0o600); err != nil {
			return e, err
		}
	}
	return e, nil
}

// Get returns evidence by ID.
func (l *Locker) Get(id string) (*Evidence, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.byID[strings.ToUpper(strings.TrimSpace(id))]
	return e, ok
}

// All returns evidence in collection order.
func (l *Locker) All() []*Evidence {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*Evidence(nil), l.items...)
}

// Missing returns the IDs in ids that are not in the locker.
func (l *Locker) Missing(ids []string) []string {
	var missing []string
	for _, id := range ids {
		if _, ok := l.Get(id); !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

func digest(e *Evidence) string {
	// Args are compacted so that re-indentation on disk does not change the seal.
	var args bytes.Buffer
	if len(e.Args) > 0 {
		if err := json.Compact(&args, e.Args); err != nil {
			args.Write(e.Args)
		}
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		e.ID, e.Tool, e.Target, args.String(), e.CollectedAt.Format(time.RFC3339Nano), e.Content, e.Error)
	return hex.EncodeToString(h.Sum(nil))
}

func idNum(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "E"))
	return n
}
