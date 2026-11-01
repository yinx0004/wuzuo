// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TranscriptFile is the name of the transcript inside a case directory.
const TranscriptFile = "transcript.jsonl"

// Transcript entry types written by the CLI around the agent's entries.
const (
	EntryCase = "case" // first line: what was asked, with which settings
	EntryEnd  = "end"  // last line: how the case ended
)

// CaseEntry opens a transcript.
type CaseEntry struct {
	Type     string         `json:"type"`
	Time     time.Time      `json:"time"`
	CaseID   string         `json:"case_id"`
	Version  string         `json:"wuzuo_version"`
	Question string         `json:"question"`
	Targets  []string       `json:"targets,omitempty"`
	ReplayOf string         `json:"replay_of,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
}

// EndEntry closes a transcript.
type EndEntry struct {
	Type   string    `json:"type"`
	Time   time.Time `json:"time"`
	Status string    `json:"status"`
	Steps  int       `json:"steps"`
	Tokens Tokens    `json:"tokens"`
	Error  string    `json:"error,omitempty"`
}

// TranscriptWriter appends JSON lines to a case's transcript and flushes
// each line to disk, so the record survives a crash or an interrupted run.
type TranscriptWriter struct {
	mu   sync.Mutex
	f    *os.File
	err  error
	path string
}

// OpenTranscript opens (or creates, mode 0600) the transcript of a case.
func (s Store) OpenTranscript(id string) (*TranscriptWriter, error) {
	path := filepath.Join(s.Dir(id), TranscriptFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path under the case directory
	if err != nil {
		return nil, err
	}
	return &TranscriptWriter{f: f, path: path}, nil
}

// Record appends v as one line. Errors are kept and reported by Err, so a
// full disk does not abort an investigation.
func (w *TranscriptWriter) Record(v any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return
	}
	line, err := json.Marshal(v)
	if err != nil {
		w.err = err
		return
	}
	if _, err := w.f.Write(append(line, '\n')); err != nil {
		w.err = err
		return
	}
	w.err = w.f.Sync()
}

// Err returns the first error encountered while writing.
func (w *TranscriptWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return fmt.Errorf("transcript %s: %w", w.path, w.err)
	}
	return nil
}

// Close closes the file.
func (w *TranscriptWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}

// ReadTranscript returns the raw lines of a case's transcript.
func (s Store) ReadTranscript(id string) ([]json.RawMessage, error) {
	full, err := s.resolve(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(s.Dir(full), TranscriptFile)) //nolint:gosec // path under the case directory
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20) // tool results can be long lines
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		lines = append(lines, append(json.RawMessage(nil), sc.Bytes()...))
	}
	return lines, sc.Err()
}
