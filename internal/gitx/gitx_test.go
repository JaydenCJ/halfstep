// Tests for the git plumbing wrapper against real repositories built in
// t.TempDir() with pinned identities and dates, so every run is
// deterministic and fully offline.
package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gitEnv pins everything that could make a commit sha vary between runs.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=Dev Example",
		"GIT_AUTHOR_EMAIL=dev@example.test",
		"GIT_COMMITTER_NAME=Dev Example",
		"GIT_COMMITTER_EMAIL=dev@example.test",
		"GIT_AUTHOR_DATE=2026-03-01T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-03-01T00:00:00Z",
	)
}

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// linearRepo builds n commits on main, each changing lib.txt, and returns
// the repo dir plus the shas oldest-first.
func linearRepo(t *testing.T, n int) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	for i := 1; i <= n; i++ {
		if err := os.WriteFile(dir+"/lib.txt", []byte(fmt.Sprintf("v%d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "lib.txt")
		git(t, dir, "commit", "-q", "-m", fmt.Sprintf("change %d", i))
	}
	shas := strings.Fields(git(t, dir, "rev-list", "--reverse", "HEAD"))
	if len(shas) != n {
		t.Fatalf("built %d commits, want %d", len(shas), n)
	}
	return dir, shas
}

func TestGitDirResolvesInsideRepo(t *testing.T) {
	dir, _ := linearRepo(t, 1)
	got, err := Git{Dir: dir}.GitDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "/.git") {
		t.Fatalf("got %q", got)
	}
}

func TestGitDirFailsOutsideRepo(t *testing.T) {
	_, err := Git{Dir: t.TempDir()}.GitDir()
	var ge *Error
	if !errors.As(err, &ge) {
		t.Fatalf("want *gitx.Error, got %v", err)
	}
	if !strings.Contains(ge.Error(), "git rev-parse") {
		t.Fatalf("error should carry the git args: %v", ge)
	}
}

func TestResolveRevisionExpressions(t *testing.T) {
	dir, shas := linearRepo(t, 5)
	g := Git{Dir: dir}
	head, err := g.Resolve("HEAD")
	if err != nil || head != shas[4] {
		t.Fatalf("HEAD: got %q, %v", head, err)
	}
	back, err := g.Resolve("HEAD~3")
	if err != nil || back != shas[1] {
		t.Fatalf("HEAD~3: got %q, %v", back, err)
	}
	prefix, err := g.Resolve(shas[2][:8])
	if err != nil || prefix != shas[2] {
		t.Fatalf("prefix: got %q, %v", prefix, err)
	}
}

func TestResolveRejectsUnknownAndNonCommit(t *testing.T) {
	dir, _ := linearRepo(t, 2)
	g := Git{Dir: dir}
	if _, err := g.Resolve("no-such-branch"); err == nil {
		t.Fatal("unknown rev should fail")
	}
	if _, err := g.Resolve("HEAD~99"); err == nil {
		t.Fatal("out-of-range rev should fail")
	}
}

func TestIsAncestor(t *testing.T) {
	dir, shas := linearRepo(t, 4)
	g := Git{Dir: dir}
	ok, err := g.IsAncestor(shas[0], shas[3])
	if err != nil || !ok {
		t.Fatalf("oldest should be ancestor of newest: %v %v", ok, err)
	}
	ok, err = g.IsAncestor(shas[3], shas[0])
	if err != nil || ok {
		t.Fatalf("newest must not be ancestor of oldest: %v %v", ok, err)
	}
	ok, err = g.IsAncestor(shas[2], shas[2])
	if err != nil || !ok {
		t.Fatalf("a commit is its own ancestor: %v %v", ok, err)
	}
}

func TestCountExcludesGoodSide(t *testing.T) {
	dir, shas := linearRepo(t, 10)
	g := Git{Dir: dir}
	n, err := g.Count(shas[9], []string{shas[0]})
	if err != nil || n != 9 {
		t.Fatalf("got %d, %v; want 9 (bad..good exclusive of good)", n, err)
	}
	n, err = g.Count(shas[9], []string{shas[0], shas[4]})
	if err != nil || n != 5 {
		t.Fatalf("two goods: got %d, %v; want 5", n, err)
	}
}

func TestRangeListNewestFirst(t *testing.T) {
	dir, shas := linearRepo(t, 6)
	g := Git{Dir: dir}
	list, err := g.RangeList(shas[5], []string{shas[1]})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{shas[5], shas[4], shas[3], shas[2]}
	if strings.Join(list, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", list, want)
	}
}

func TestRangeListEmptyRange(t *testing.T) {
	dir, shas := linearRepo(t, 3)
	g := Git{Dir: dir}
	list, err := g.RangeList(shas[0], []string{shas[2]})
	if err != nil || len(list) != 0 {
		t.Fatalf("got %v, %v; want empty", list, err)
	}
}

// TestBisectOrderFirstEntryHalvesTheRange: the top-ranked candidate of a
// 15-commit linear range must be the 8th commit (splits 15 into 7+1+7),
// and decorations like "(HEAD -> main, dist=0)" must be stripped.
func TestBisectOrderFirstEntryHalvesTheRange(t *testing.T) {
	dir, shas := linearRepo(t, 16)
	g := Git{Dir: dir}
	order, err := g.BisectOrder(shas[15], []string{shas[0]})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 15 {
		t.Fatalf("got %d candidates, want 15", len(order))
	}
	if order[0] != shas[8] {
		t.Fatalf("best split: got %s, want %s (the middle)", order[0], shas[8])
	}
	for _, sha := range order {
		if len(sha) != 40 || strings.ContainsAny(sha, " ()") {
			t.Fatalf("unparsed decoration in %q", sha)
		}
	}
}

func TestCheckoutDetachesAndCurrentRef(t *testing.T) {
	dir, shas := linearRepo(t, 3)
	g := Git{Dir: dir}
	ref, err := g.CurrentRef()
	if err != nil || ref != "main" {
		t.Fatalf("on a branch: got %q, %v", ref, err)
	}
	if err := g.Checkout(shas[1]); err != nil {
		t.Fatal(err)
	}
	head, _ := g.Resolve("HEAD")
	if head != shas[1] {
		t.Fatalf("HEAD at %s, want %s", head, shas[1])
	}
	ref, err = g.CurrentRef()
	if err != nil || ref != shas[1] {
		t.Fatalf("detached: got %q, %v; want the sha", ref, err)
	}
	if err := g.CheckoutRef("main"); err != nil {
		t.Fatal(err)
	}
	ref, _ = g.CurrentRef()
	if ref != "main" {
		t.Fatalf("restore: got %q", ref)
	}
}

func TestIsDirty(t *testing.T) {
	dir, _ := linearRepo(t, 2)
	g := Git{Dir: dir}
	dirty, err := g.IsDirty()
	if err != nil || dirty {
		t.Fatalf("clean tree reported dirty: %v %v", dirty, err)
	}
	if err := os.WriteFile(dir+"/lib.txt", []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = g.IsDirty()
	if err != nil || !dirty {
		t.Fatalf("modified tracked file not reported: %v %v", dirty, err)
	}
	// Untracked files do not block checkouts, so they must not count.
	git(t, dir, "checkout", "-q", "--", "lib.txt")
	if err := os.WriteFile(dir+"/scratch.txt", []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = g.IsDirty()
	if err != nil || dirty {
		t.Fatalf("untracked file should not count as dirty: %v %v", dirty, err)
	}
}

func TestInfoFields(t *testing.T) {
	dir, shas := linearRepo(t, 2)
	g := Git{Dir: dir}
	info, err := g.Info(shas[1])
	if err != nil {
		t.Fatal(err)
	}
	if info.Hash != shas[1] || info.Short != shas[1][:7] {
		t.Fatalf("hash fields wrong: %+v", info)
	}
	if info.Author != "Dev Example <dev@example.test>" {
		t.Fatalf("author: %q", info.Author)
	}
	if info.Date != "2026-03-01" {
		t.Fatalf("date: %q", info.Date)
	}
	if info.Subject != "change 2" {
		t.Fatalf("subject: %q", info.Subject)
	}
}
