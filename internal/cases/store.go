// SPDX-License-Identifier: Apache-2.0

// Package cases persists investigations ("cases") on disk:
//
//	<root>/<case-id>/case.json      metadata, hypotheses, status
//	<root>/<case-id>/evidence/E*.json sealed evidence
//	<root>/<case-id>/report.md|json  the autopsy report
package cases

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yinx0004/wuzuo/internal/report"
)

// Status of a case.
const (
	StatusOpen   = "open"
	StatusClosed = "closed"
	StatusFailed = "failed"
)

// Hypothesis is a candidate cause tracked during an investigation.
type Hypothesis struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Rationale string   `json:"rationale,omitempty"`
	Status    string   `json:"status"` // open | ruled_out
	Reason    string   `json:"reason,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
}

// Tokens is aggregate model usage.
type Tokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	CacheRead int64 `json:"cache_read,omitempty"`
}

// Case is the metadata of one investigation.
type Case struct {
	ID         string       `json:"id"`
	Question   string       `json:"question"`
	Targets    []string     `json:"targets"`
	Provider   string       `json:"provider"`
	Model      string       `json:"model"`
	Started    time.Time    `json:"started"`
	Finished   time.Time    `json:"finished,omitempty"`
	Status     string       `json:"status"`
	Error      string       `json:"error,omitempty"`
	Steps      int          `json:"steps"`
	Tokens     Tokens       `json:"tokens"`
	Hypotheses []Hypothesis `json:"hypotheses,omitempty"`
	// ReplayOf is set when this case re-examined another case's evidence.
	ReplayOf string `json:"replay_of,omitempty"`
}

// Store manages cases under Root.
type Store struct{ Root string }

// NewID returns a sortable, unique case ID such as 20261101-153012-a1b2.
func NewID(now time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// Dir returns the directory of a case.
func (s Store) Dir(id string) string { return filepath.Join(s.Root, id) }

// EvidenceDir returns the evidence directory of a case.
func (s Store) EvidenceDir(id string) string { return filepath.Join(s.Dir(id), "evidence") }

// Save writes case.json.
func (s Store) Save(c *Case) error {
	if err := os.MkdirAll(s.Dir(c.ID), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir(c.ID), "case.json"), data, 0o600)
}

// Load reads a case. id may be a unique prefix.
func (s Store) Load(id string) (*Case, error) {
	full, err := s.resolve(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(full), "case.json"))
	if err != nil {
		return nil, err
	}
	var c Case
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// List returns cases, newest first.
func (s Store) List() ([]*Case, error) {
	entries, err := os.ReadDir(s.Root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := s.Load(e.Name())
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// SaveReport writes report.json and report.md.
func (s Store) SaveReport(id string, a *report.Autopsy, markdown string) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Dir(id), "report.json"), data, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir(id), "report.md"), []byte(markdown), 0o600)
}

// LoadReport reads report.json.
func (s Store) LoadReport(id string) (*report.Autopsy, error) {
	full, err := s.resolve(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.Dir(full), "report.json"))
	if err != nil {
		return nil, err
	}
	var a report.Autopsy
	return &a, json.Unmarshal(data, &a)
}

func (s Store) resolve(id string) (string, error) {
	if _, err := os.Stat(filepath.Join(s.Dir(id), "case.json")); err == nil {
		return id, nil
	}
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return "", fmt.Errorf("case %q not found", id)
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) >= len(id) && e.Name()[:len(id)] == id {
			matches = append(matches, e.Name())
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("case %q not found", id)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("case id %q is ambiguous (%d matches)", id, len(matches))
	}
}
