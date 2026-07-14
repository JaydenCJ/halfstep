// Tests for session persistence: round-tripping, the missing-session
// sentinel, corruption and schema-mismatch handling, and idempotent
// deletion. All I/O happens in t.TempDir().
package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() *State {
	return &State{
		OriginalRef:  "main",
		InitialBad:   "b1",
		InitialGoods: []string{"g1"},
		InitialCount: 8,
		Bad:          "b2",
		Goods:        []string{"g1", "g2"},
		Skipped:      []string{"s1"},
		Current:      "c1",
		Steps: []Step{
			{Commit: "g2", Verdict: "good", Before: 8, After: 4, At: "2026-07-13T00:00:00Z"},
		},
	}
}

func TestPathUnderGitDir(t *testing.T) {
	got := Path("/repo/.git")
	want := filepath.Join("/repo/.git", "halfstep", "state.json")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLoadMissingReturnsErrNoSession(t *testing.T) {
	_, err := Load(t.TempDir())
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v, want ErrNoSession", err)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	st := sample()
	if err := st.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version %d", got.SchemaVersion)
	}
	if got.Bad != "b2" || got.Current != "c1" || got.OriginalRef != "main" {
		t.Fatalf("fields lost: %+v", got)
	}
	if len(got.Steps) != 1 || got.Steps[0].After != 4 {
		t.Fatalf("steps lost: %+v", got.Steps)
	}
	if len(got.Goods) != 2 || !got.IsSkipped("s1") {
		t.Fatalf("marks lost: %+v", got)
	}
}

func TestSaveOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	st := sample()
	if err := st.Save(dir); err != nil {
		t.Fatal(err)
	}
	st.Bad = "b3"
	if err := st.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || got.Bad != "b3" {
		t.Fatalf("got %+v, %v", got, err)
	}
	// The temp file must have been renamed away, not left behind.
	entries, err := os.ReadDir(filepath.Join(dir, "halfstep"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("stray files after save: %v", entries)
	}
}

func TestLoadCorruptFileFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("got %v, want corrupt-state error", err)
	}
}

func TestLoadWrongSchemaVersionFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte(`{"schema_version": 99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("got %v, want schema-version error", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if err := Delete(dir); err != nil {
		t.Fatalf("deleting a non-existent session must not error: %v", err)
	}
	if err := sample().Save(dir); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session should be gone, got %v", err)
	}
}

func TestIsSkipped(t *testing.T) {
	st := sample()
	if !st.IsSkipped("s1") {
		t.Fatal("s1 should be skipped")
	}
	if st.IsSkipped("g1") {
		t.Fatal("g1 should not be skipped")
	}
	if (&State{}).IsSkipped("x") {
		t.Fatal("empty state skips nothing")
	}
}

func TestSaveEndsWithNewline(t *testing.T) {
	dir := t.TempDir()
	if err := sample().Save(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatal("state.json should be newline-terminated for cat-friendliness")
	}
}
