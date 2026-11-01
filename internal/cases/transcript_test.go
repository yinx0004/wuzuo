// SPDX-License-Identifier: Apache-2.0

package cases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTranscriptWriter(t *testing.T) {
	s := Store{Root: t.TempDir()}
	if err := s.Save(&Case{ID: "c1"}); err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenTranscript("c1")
	if err != nil {
		t.Fatal(err)
	}
	w.Record(CaseEntry{Type: EntryCase, CaseID: "c1", Question: "q"})
	w.Record(map[string]any{"type": "user", "text": "line\nwith newline"})
	w.Record(EndEntry{Type: EntryEnd, Status: StatusClosed})
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	st, err := os.Stat(filepath.Join(s.Dir("c1"), TranscriptFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st.Mode().Perm(), err)
	}
	lines, err := s.ReadTranscript("c")
	if err != nil || len(lines) != 3 {
		t.Fatalf("%d lines, %v", len(lines), err)
	}
	var e map[string]any
	if err := json.Unmarshal(lines[1], &e); err != nil || e["text"] != "line\nwith newline" {
		t.Fatalf("line 2: %v %v", e, err)
	}

	// Appending continues the same file (e.g. a resumed writer).
	w2, _ := s.OpenTranscript("c1")
	w2.Record(map[string]any{"type": "extra"})
	_ = w2.Close()
	if lines, _ := s.ReadTranscript("c1"); len(lines) != 4 {
		t.Fatalf("append: %d lines", len(lines))
	}
}
