// Package state persists a bisect session as one JSON document inside the
// repository's .git directory (never in the working tree, so it can never
// dirty a checkout or end up committed). Writes are atomic: temp file in
// the same directory, then rename.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion is bumped whenever the on-disk layout changes shape.
const SchemaVersion = 1

// ErrNoSession is returned by Load when no bisect session exists.
var ErrNoSession = errors.New("no bisect session in progress")

// Step records one verdict and how it shrank the range.
type Step struct {
	Commit  string `json:"commit"`  // full sha the verdict applies to
	Verdict string `json:"verdict"` // "good", "bad", or "skip"
	Before  int    `json:"before"`  // candidates before this verdict
	After   int    `json:"after"`   // candidates after this verdict
	At      string `json:"at"`      // RFC 3339 UTC timestamp
}

// State is the whole session: the fixed endpoints chosen at start, the
// current shrinking marks, and the full step history (which is what makes
// undo and the shrink chart possible).
type State struct {
	SchemaVersion int      `json:"schema_version"`
	OriginalRef   string   `json:"original_ref"`  // branch or sha to restore on reset
	InitialBad    string   `json:"initial_bad"`   // bad endpoint at start
	InitialGoods  []string `json:"initial_goods"` // good endpoints at start
	InitialCount  int      `json:"initial_count"` // candidates at start
	Bad           string   `json:"bad"`           // current bad frontier
	Goods         []string `json:"goods"`         // current good frontier
	Skipped       []string `json:"skipped"`       // untestable commits
	Current       string   `json:"current"`       // commit checked out for testing ("" once done)
	Steps         []Step   `json:"steps"`
}

// Path returns the state file location under the given .git directory.
func Path(gitDir string) string {
	return filepath.Join(gitDir, "halfstep", "state.json")
}

// Load reads the session, returning ErrNoSession when the file is absent.
func Load(gitDir string) (*State, error) {
	raw, err := os.ReadFile(Path(gitDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoSession
		}
		return nil, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("corrupt state file %s: %w", Path(gitDir), err)
	}
	if st.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("state file schema version %d is not %d; run 'halfstep reset' and start again", st.SchemaVersion, SchemaVersion)
	}
	return &st, nil
}

// Save writes the session atomically.
func (s *State) Save(gitDir string) error {
	s.SchemaVersion = SchemaVersion
	path := Path(gitDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "state-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Delete removes the session; deleting a non-existent session is not an
// error, so reset stays idempotent.
func Delete(gitDir string) error {
	err := os.Remove(Path(gitDir))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// IsSkipped reports whether sha carries a skip verdict.
func (s *State) IsSkipped(sha string) bool {
	for _, sk := range s.Skipped {
		if sk == sha {
			return true
		}
	}
	return false
}
