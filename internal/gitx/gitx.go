// Package gitx is the only place halfstep talks to git. It shells out to
// the git binary (plumbing commands only, never `git bisect`) so halfstep
// works against any repository git itself can read, including ones with
// merges, octopus history, and shallow-ish edge cases git already solved.
// Everything else in halfstep is pure logic over the strings returned here.
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Git runs git commands inside Dir. The zero value runs in the process's
// working directory.
type Git struct {
	Dir string
}

// Error wraps a failed git invocation with enough context to print an
// actionable message: the args that ran and what git said on stderr.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

// run executes git with args and returns stdout with the trailing newline
// trimmed. Non-zero exits become *Error carrying git's stderr.
func (g Git) run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.Dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: errb.String(), Err: err}
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// GitDir resolves the absolute .git directory of the repository, which is
// where halfstep keeps its session state. Returns an error outside a repo.
func (g Git) GitDir() (string, error) {
	return g.run("rev-parse", "--absolute-git-dir")
}

// Resolve turns any revision expression (branch, tag, sha prefix, HEAD~3)
// into a full commit sha, rejecting non-commit objects.
func (g Git) Resolve(rev string) (string, error) {
	out, err := g.run("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve %q to a commit", rev)
	}
	return out, nil
}

// IsAncestor reports whether ancestor is reachable from descendant
// (a commit counts as its own ancestor, matching git merge-base).
func (g Git) IsAncestor(ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = g.Dir
	var errb bytes.Buffer
	cmd.Stderr = &errb
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, &Error{Args: []string{"merge-base", "--is-ancestor"}, Stderr: errb.String(), Err: err}
}

// notArgs expands (bad, goods) into rev-list arguments: bad --not good....
func notArgs(bad string, goods []string) []string {
	args := []string{bad, "--not"}
	return append(args, goods...)
}

// Count returns the number of commits reachable from bad but from no good
// commit — the set of candidates for "first bad commit", bad included.
func (g Git) Count(bad string, goods []string) (int, error) {
	out, err := g.run(append([]string{"rev-list", "--count"}, notArgs(bad, goods)...)...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("unexpected rev-list --count output %q", out)
	}
	return n, nil
}

// RangeList returns the candidate commits (newest first), the same set
// Count measures. Used to build the range map for visualization.
func (g Git) RangeList(bad string, goods []string) ([]string, error) {
	out, err := g.run(append([]string{"rev-list"}, notArgs(bad, goods)...)...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// BisectOrder returns the candidates ranked best-split-first, exactly as
// git's own bisection would order them (`rev-list --bisect-all`). Each
// output line looks like "<sha> (dist=N)"; only the sha is kept. The first
// usable entry halves the remaining range as evenly as the DAG allows.
func (g Git) BisectOrder(bad string, goods []string) ([]string, error) {
	out, err := g.run(append([]string{"rev-list", "--bisect-all"}, notArgs(bad, goods)...)...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	shas := make([]string, 0, len(lines))
	for _, ln := range lines {
		if fields := strings.Fields(ln); len(fields) > 0 {
			shas = append(shas, fields[0])
		}
	}
	return shas, nil
}

// Checkout detaches HEAD at rev, quietly. halfstep never moves branches.
func (g Git) Checkout(rev string) error {
	_, err := g.run("checkout", "--quiet", "--detach", rev)
	return err
}

// CheckoutRef restores a branch (or detached sha) recorded at start time.
func (g Git) CheckoutRef(ref string) error {
	_, err := g.run("checkout", "--quiet", ref)
	return err
}

// CurrentRef returns the current branch name, or the HEAD sha when
// detached, so a session can be reset back to where the user started.
func (g Git) CurrentRef() (string, error) {
	out, err := g.run("symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil && out != "" {
		return out, nil
	}
	return g.run("rev-parse", "HEAD")
}

// IsDirty reports whether tracked files have uncommitted changes; halfstep
// refuses to start checkouts over a dirty tree unless forced.
func (g Git) IsDirty() (bool, error) {
	out, err := g.run("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// CommitInfo is the human-facing identity of one commit.
type CommitInfo struct {
	Hash    string // full sha
	Short   string // abbreviated sha
	Author  string // "Name <email>"
	Date    string // author date, YYYY-MM-DD
	Subject string // first line of the message
}

// Info loads CommitInfo for rev in one git call, NUL-separated so subjects
// containing any printable character round-trip safely.
func (g Git) Info(rev string) (CommitInfo, error) {
	out, err := g.run("show", "--no-patch", "--format=%H%x00%h%x00%an <%ae>%x00%as%x00%s", rev)
	if err != nil {
		return CommitInfo{}, err
	}
	parts := strings.SplitN(out, "\x00", 5)
	if len(parts) != 5 {
		return CommitInfo{}, fmt.Errorf("unexpected git show output for %s", rev)
	}
	return CommitInfo{Hash: parts[0], Short: parts[1], Author: parts[2], Date: parts[3], Subject: parts[4]}, nil
}
