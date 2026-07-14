// Tests for the bisection engine against real git repositories. Repos are
// built with a single deterministic `git fast-import` stream (pinned
// identity and timestamps), then the engine hunts a planted bug and the
// tests assert it converges on exactly the right commit — including
// through merges, skips, contradictions, and undo.
package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/JaydenCJ/halfstep/internal/gitx"
	"github.com/JaydenCJ/halfstep/internal/state"
)

// gitRun executes git in dir with a pinned environment.
func gitRun(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Dev Example", "GIT_AUTHOR_EMAIL=dev@example.test",
		"GIT_COMMITTER_NAME=Dev Example", "GIT_COMMITTER_EMAIL=dev@example.test",
	)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// fiCommit appends one fast-import commit to b. Every commit writes a
// varying lib.txt plus a constant NOTES.md (so tests can dirty a tracked
// file that never conflicts with checkouts).
func fiCommit(b *strings.Builder, mark int, branch, subject, content string, parents ...string) {
	fmt.Fprintf(b, "blob\nmark :%d\ndata %d\n%s\n", mark*2-1, len(content), content)
	fmt.Fprintf(b, "commit refs/heads/%s\nmark :%d\n", branch, mark*2)
	when := fmt.Sprintf("%d +0000", 1774000000+mark*60) // strictly increasing, pinned
	fmt.Fprintf(b, "author Dev Example <dev@example.test> %s\n", when)
	fmt.Fprintf(b, "committer Dev Example <dev@example.test> %s\n", when)
	fmt.Fprintf(b, "data %d\n%s\n", len(subject), subject)
	for i, p := range parents {
		verb := "from"
		if i > 0 {
			verb = "merge"
		}
		fmt.Fprintf(b, "%s %s\n", verb, p)
	}
	fmt.Fprintf(b, "M 100644 :%d lib.txt\n", mark*2-1)
	fmt.Fprintf(b, "M 100644 inline NOTES.md\ndata 6\nnotes\n\n")
}

// linearRepo builds n commits on main; commits >= bugAt (1-based) contain
// the bug marker in lib.txt. Returns the repo dir and shas oldest-first.
func linearRepo(t *testing.T, n, bugAt int) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "", "init", "-q", "-b", "main")
	var b strings.Builder
	for i := 1; i <= n; i++ {
		content := fmt.Sprintf("v%d", i)
		if bugAt > 0 && i >= bugAt {
			content = fmt.Sprintf("v%d BUG", i)
		}
		fiCommit(&b, i, "main", fmt.Sprintf("change %d", i), content)
	}
	gitRun(t, dir, b.String(), "fast-import", "--quiet")
	gitRun(t, dir, "", "reset", "--hard", "-q", "main")
	shas := strings.Fields(gitRun(t, dir, "", "rev-list", "--reverse", "HEAD"))
	if len(shas) != n {
		t.Fatalf("built %d commits, want %d", len(shas), n)
	}
	return dir, shas
}

// isBad reports whether the checked-out worktree contains the bug marker.
func isBad(t *testing.T, dir string) bool {
	t.Helper()
	raw, err := os.ReadFile(dir + "/lib.txt")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), "BUG")
}

// hunt drives the engine to completion by consulting the worktree, and
// returns the final progress. Bounded to guard against livelock bugs.
func hunt(t *testing.T, dir string, e *Engine, p *Progress) *Progress {
	t.Helper()
	for i := 0; p.Next != "" && i < 100; i++ {
		verdict := "good"
		if isBad(t, dir) {
			verdict = "bad"
		}
		_, next, err := e.Mark(verdict, "")
		if err != nil {
			t.Fatal(err)
		}
		p = next
	}
	if p.Next != "" {
		t.Fatal("hunt did not terminate in 100 steps")
	}
	return p
}

func TestStartChecksOutTheMidpoint(t *testing.T) {
	dir, shas := linearRepo(t, 16, 0)
	e, p, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 15 || p.InitialCount != 15 {
		t.Fatalf("count: %+v", p)
	}
	if p.Next != shas[8] {
		t.Fatalf("midpoint: got %s, want %s", p.Next, shas[8])
	}
	head, _ := e.G.Resolve("HEAD")
	if head != p.Next {
		t.Fatal("HEAD should be on the commit under test")
	}
	if e.St.Current != p.Next || e.St.OriginalRef != "main" {
		t.Fatalf("state: %+v", e.St)
	}
}

func TestStartPersistsAndOpens(t *testing.T) {
	dir, shas := linearRepo(t, 8, 0)
	if _, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false); err != nil {
		t.Fatal(err)
	}
	e, err := Open(gitx.Git{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if e.St.InitialBad != shas[7] || len(e.St.InitialGoods) != 1 {
		t.Fatalf("reloaded state wrong: %+v", e.St)
	}
}

func TestStartRefusesSecondSession(t *testing.T) {
	dir, shas := linearRepo(t, 4, 0)
	if _, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false); err != nil {
		t.Fatal(err)
	}
	_, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("got %v", err)
	}
}

func TestStartRejectsNonAncestorGood(t *testing.T) {
	dir, shas := linearRepo(t, 5, 0)
	// good newer than bad: not an ancestor.
	_, _, err := Start(gitx.Git{Dir: dir}, shas[1], []string{shas[4]}, false)
	if err == nil || !strings.Contains(err.Error(), "not an ancestor") {
		t.Fatalf("got %v", err)
	}
}

func TestStartRejectsGoodEqualsBad(t *testing.T) {
	dir, shas := linearRepo(t, 3, 0)
	_, _, err := Start(gitx.Git{Dir: dir}, shas[2], []string{shas[2]}, false)
	if !errors.Is(err, ErrContradiction) {
		t.Fatalf("got %v, want ErrContradiction", err)
	}
}

func TestStartRefusesDirtyTreeUnlessForced(t *testing.T) {
	dir, shas := linearRepo(t, 6, 0)
	// NOTES.md is tracked but identical in every commit, so the checkout
	// itself cannot conflict — exactly the case --force exists for.
	if err := os.WriteFile(dir+"/NOTES.md", []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if !errors.Is(err, ErrDirty) {
		t.Fatalf("got %v, want ErrDirty", err)
	}
	if _, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, true); err != nil {
		t.Fatalf("forced start failed: %v", err)
	}
	raw, err := os.ReadFile(dir + "/NOTES.md")
	if err != nil || string(raw) != "edited\n" {
		t.Fatalf("local edit should survive the forced checkout: %q %v", raw, err)
	}
}

func TestStartDegenerateSingleCommitRangeIsDoneImmediately(t *testing.T) {
	dir, shas := linearRepo(t, 2, 2)
	_, p, err := Start(gitx.Git{Dir: dir}, shas[1], []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Done || p.Culprit != shas[1] {
		t.Fatalf("one-commit range should finish at start: %+v", p)
	}
}

// TestHuntFindsPlantedBugLinear is the core promise: for every possible
// bug position in a 12-commit history, the hunt converges on exactly that
// commit using only good/bad verdicts from the worktree.
func TestHuntFindsPlantedBugLinear(t *testing.T) {
	for bugAt := 2; bugAt <= 12; bugAt++ {
		dir, shas := linearRepo(t, 12, bugAt)
		e, p, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
		if err != nil {
			t.Fatalf("bugAt=%d: %v", bugAt, err)
		}
		p = hunt(t, dir, e, p)
		if !p.Done || p.Culprit != shas[bugAt-1] {
			t.Fatalf("bugAt=%d: culprit %s, want %s", bugAt, p.Culprit, shas[bugAt-1])
		}
		if p.Count != 1 {
			t.Fatalf("bugAt=%d: %d candidates left", bugAt, p.Count)
		}
	}
}

// TestHuntStaysWithinTheHalvingBudget: 100 candidates must never need
// more than ceil(log2(100)) = 7 verdicts.
func TestHuntStaysWithinTheHalvingBudget(t *testing.T) {
	dir, shas := linearRepo(t, 101, 37)
	e, p, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.StepsLeftEstimate(); got != 7 {
		t.Fatalf("estimate for 100 candidates: got %d, want 7", got)
	}
	p = hunt(t, dir, e, p)
	if !p.Done || p.Culprit != shas[36] {
		t.Fatalf("culprit %s, want %s", p.Culprit, shas[36])
	}
	if len(e.St.Steps) > 7 {
		t.Fatalf("took %d steps for 100 candidates, budget is 7", len(e.St.Steps))
	}
}

// mergeRepo builds a diamond: main has 3 commits, a side branch forks
// after the first, plants the bug, and merges back before two more main
// commits. The culprit lives on the side branch.
//
//	m1 ── m2 ── m3 ──── M ── m4 (bad)
//	  \                /
//	   s1 ── s2(BUG) ─
func mergeRepo(t *testing.T) (dir string, culprit string, first string, tip string) {
	t.Helper()
	dir = t.TempDir()
	gitRun(t, dir, "", "init", "-q", "-b", "main")
	var b strings.Builder
	fiCommit(&b, 1, "main", "m1", "m1")
	fiCommit(&b, 2, "main", "m2", "m2")
	fiCommit(&b, 3, "main", "m3", "m3")
	fiCommit(&b, 4, "side", "s1", "s1", ":2")     // fork from m1
	fiCommit(&b, 5, "side", "s2", "s2 BUG", ":8") // the culprit
	fiCommit(&b, 6, "main", "merge side", "m3 BUG", ":6", ":10")
	fiCommit(&b, 7, "main", "m4", "m4 BUG")
	gitRun(t, dir, b.String(), "fast-import", "--quiet")
	gitRun(t, dir, "", "reset", "--hard", "-q", "main")
	rev := func(spec string) string { return gitRun(t, dir, "", "rev-parse", spec) }
	// culprit s2 is the merge's second parent; m1 is 4 first-parent hops back.
	return dir, rev("main~1^2"), rev("main~4"), rev("main")
}

func TestHuntFindsBugIntroducedOnMergedBranch(t *testing.T) {
	dir, culprit, first, _ := mergeRepo(t)
	e, p, err := Start(gitx.Git{Dir: dir}, "main", []string{first}, false)
	if err != nil {
		t.Fatal(err)
	}
	p = hunt(t, dir, e, p)
	if !p.Done || p.Culprit != culprit {
		t.Fatalf("culprit %s, want %s (the side-branch commit)", p.Culprit, culprit)
	}
}

func TestMarkGoodShrinksAndAdvances(t *testing.T) {
	dir, shas := linearRepo(t, 16, 0)
	e, p, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	step, next, err := e.Mark("good", "")
	if err != nil {
		t.Fatal(err)
	}
	if step.Before != 15 || step.After != 7 {
		t.Fatalf("good on the midpoint of 15 should leave 7: %+v", step)
	}
	if next.Next == p.Next || next.Next == "" {
		t.Fatalf("should advance to a new commit, got %q", next.Next)
	}
	head, _ := e.G.Resolve("HEAD")
	if head != next.Next {
		t.Fatal("HEAD should follow the commit under test")
	}
}

func TestMarkExplicitRevisionInsteadOfCurrent(t *testing.T) {
	dir, shas := linearRepo(t, 10, 0)
	e, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Mark a specific commit bad, not the one under test.
	step, p, err := e.Mark("bad", shas[3])
	if err != nil {
		t.Fatal(err)
	}
	if step.Commit != shas[3] {
		t.Fatalf("marked %s, want %s", step.Commit, shas[3])
	}
	if p.Count != 3 { // shas[1..3] remain
		t.Fatalf("count %d, want 3", p.Count)
	}
}

func TestMarkBadOutsideRangeIsRejected(t *testing.T) {
	dir, shas := linearRepo(t, 10, 0)
	e, _, err := Start(gitx.Git{Dir: dir}, shas[7], []string{shas[2]}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Mark("bad", shas[9]); err == nil {
		t.Fatal("marking bad outside the range must be rejected")
	}
	if _, _, err := e.Mark("skip", shas[1]); err == nil {
		t.Fatal("skipping outside the range must be rejected")
	}
	// The rejected marks must leave the session untouched.
	if e.St.Bad != shas[7] || len(e.St.Steps) != 0 || len(e.St.Skipped) != 0 {
		t.Fatalf("state mutated by rejected mark: %+v", e.St)
	}
}

func TestMarkBadCommitGoodIsContradiction(t *testing.T) {
	dir, shas := linearRepo(t, 6, 0)
	e, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.Mark("good", shas[5])
	if !errors.Is(err, ErrContradiction) {
		t.Fatalf("got %v, want ErrContradiction", err)
	}
	if len(e.St.Goods) != 1 {
		t.Fatal("contradictory mark must not persist")
	}
}

func TestSkipMovesToAnotherCandidate(t *testing.T) {
	dir, shas := linearRepo(t, 16, 0)
	e, p, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	first := p.Next
	step, next, err := e.Mark("skip", "")
	if err != nil {
		t.Fatal(err)
	}
	if step.Before != step.After {
		t.Fatalf("skip must not change the count: %+v", step)
	}
	if next.Next == first || next.Next == "" {
		t.Fatalf("skip should move to a different candidate, got %q", next.Next)
	}
}

func TestAllSkippedIsInconclusiveWithSuspects(t *testing.T) {
	dir, shas := linearRepo(t, 4, 0) // range: shas[1..3], 3 candidates
	e, p, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	for p.Next != "" {
		_, p, err = e.Mark("skip", "")
		if err != nil {
			t.Fatal(err)
		}
	}
	if !p.Inconclusive || p.Done {
		t.Fatalf("want inconclusive: %+v", p)
	}
	if len(p.Suspects) != 3 {
		t.Fatalf("suspects: got %d, want 3", len(p.Suspects))
	}
}

func TestSkipBadEndpointIsRejected(t *testing.T) {
	dir, shas := linearRepo(t, 4, 0)
	e, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Mark("skip", shas[3]); err == nil {
		t.Fatal("skipping the bad endpoint should be rejected")
	}
}

func TestUnknownVerdictIsRejected(t *testing.T) {
	dir, shas := linearRepo(t, 4, 0)
	e, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Mark("meh", ""); err == nil {
		t.Fatal("unknown verdict should be rejected")
	}
}

func TestUndoRestoresThePreviousRange(t *testing.T) {
	dir, shas := linearRepo(t, 16, 0)
	e, p0, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	firstPick := p0.Next
	if _, _, err := e.Mark("good", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Mark("bad", ""); err != nil {
		t.Fatal(err)
	}
	undone, p, err := e.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if undone.Verdict != "bad" {
		t.Fatalf("undid %q, want the last verdict (bad)", undone.Verdict)
	}
	if p.Count != 7 || len(e.St.Steps) != 1 {
		t.Fatalf("after undo: count %d steps %d, want 7 and 1", p.Count, len(e.St.Steps))
	}
	// Undo the remaining step too: back to the very start.
	_, p, err = e.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 15 || p.Next != firstPick {
		t.Fatalf("full undo should restore the first pick: %+v", p)
	}
	if _, _, err := e.Undo(); err == nil {
		t.Fatal("undo with no steps should error")
	}
}

func TestUndoRestoresSkips(t *testing.T) {
	dir, shas := linearRepo(t, 8, 0)
	e, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Mark("skip", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Mark("good", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Undo(); err != nil { // undoes the good
		t.Fatal(err)
	}
	if len(e.St.Skipped) != 1 {
		t.Fatalf("skip from step 1 must survive undoing step 2: %+v", e.St.Skipped)
	}
}

func TestRangeFlagsMapSurvivorsOntoInitialRange(t *testing.T) {
	dir, shas := linearRepo(t, 11, 0) // range shas[1..10], 10 candidates
	e, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	// Cut the range to shas[3..6]: good up to shas[2], bad from shas[6].
	if _, _, err := e.Mark("good", shas[2]); err != nil {
		t.Fatal(err)
	}
	_, p, err := e.Mark("bad", shas[6])
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{false, false, true, true, true, true, false, false, false, false}
	if len(p.RangeFlags) != len(want) {
		t.Fatalf("flags length %d, want %d", len(p.RangeFlags), len(want))
	}
	for i := range want {
		if p.RangeFlags[i] != want[i] {
			t.Fatalf("flags[%d] = %v, want %v (%v)", i, p.RangeFlags[i], want[i], p.RangeFlags)
		}
	}
}

func TestResetRestoresBranchAndDeletesState(t *testing.T) {
	dir, shas := linearRepo(t, 8, 0)
	e, _, err := Start(gitx.Git{Dir: dir}, "HEAD", []string{shas[0]}, false)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := e.Reset()
	if err != nil || ref != "main" {
		t.Fatalf("got %q, %v", ref, err)
	}
	if out := gitRun(t, dir, "", "symbolic-ref", "--short", "HEAD"); out != "main" {
		t.Fatalf("HEAD on %q, want main", out)
	}
	if _, err := state.Load(e.GitDir); !errors.Is(err, state.ErrNoSession) {
		t.Fatalf("state should be gone, got %v", err)
	}
}

func TestStepsLeftEstimate(t *testing.T) {
	cases := map[int]int{1: 0, 2: 1, 3: 2, 4: 2, 5: 3, 8: 3, 9: 4, 100: 7, 1024: 10}
	for n, want := range cases {
		p := Progress{Count: n}
		if got := p.StepsLeftEstimate(); got != want {
			t.Fatalf("StepsLeftEstimate(%d) = %d, want %d", n, got, want)
		}
	}
}
