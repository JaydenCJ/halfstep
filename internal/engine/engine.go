// Package engine holds the bisection logic: which commit to test next,
// what a verdict does to the range, and when the hunt is over. It never
// implements graph search itself — git's own `rev-list --bisect-all`
// ranks the candidates — but it owns all session semantics: validation,
// skip handling, contradiction detection, undo, and termination.
package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/JaydenCJ/halfstep/internal/gitx"
	"github.com/JaydenCJ/halfstep/internal/state"
)

// Sentinel errors the CLI maps to friendly messages and exit code 1.
var (
	// ErrContradiction: the marks cannot all be true (e.g. the bad commit
	// was marked good, or a "good" descends from the current bad).
	ErrContradiction = errors.New("marks contradict each other")
	// ErrDirty: tracked files have uncommitted changes and checkouts
	// would clobber or collide with them.
	ErrDirty = errors.New("working tree has uncommitted changes")
)

// Progress is a full snapshot of where the hunt stands, recomputed from
// the marks via git on every query so it can never drift from reality.
type Progress struct {
	Count        int      // candidates still possible
	InitialCount int      // candidates at session start
	Next         string   // commit to test now ("" when nothing testable)
	Done         bool     // exactly one candidate remains
	Culprit      string   // the first bad commit, when Done
	Inconclusive bool     // >1 candidates but all untestable (skips)
	Suspects     []string // remaining candidates when Inconclusive
	RangeFlags   []bool   // initial range oldest→newest; true = still a candidate
}

// StepsLeftEstimate returns the expected number of verdicts still needed:
// ceil(log2(count)), the halving budget. Zero when the hunt is over.
func (p *Progress) StepsLeftEstimate() int {
	return stepsFor(p.Count)
}

func stepsFor(n int) int {
	if n <= 1 {
		return 0
	}
	steps := 0
	for x := n - 1; x > 0; x >>= 1 { // ceil(log2(n)) via bit length of n-1
		steps++
	}
	return steps
}

// Engine binds a git repository to a persisted session.
type Engine struct {
	G      gitx.Git
	GitDir string
	St     *state.State
}

// Open loads the existing session for the repository at g.Dir.
func Open(g gitx.Git) (*Engine, error) {
	gitDir, err := g.GitDir()
	if err != nil {
		return nil, err
	}
	st, err := state.Load(gitDir)
	if err != nil {
		return nil, err
	}
	return &Engine{G: g, GitDir: gitDir, St: st}, nil
}

// Start validates the endpoints, records the session, and checks out the
// first commit to test. goods must each be an ancestor of bad — halfstep
// v0.1.0 keeps the classic "regression somewhere on the path" contract
// rather than git bisect's more exotic non-ancestor handling.
func Start(g gitx.Git, badRev string, goodRevs []string, force bool) (*Engine, *Progress, error) {
	gitDir, err := g.GitDir()
	if err != nil {
		return nil, nil, err
	}
	if _, err := state.Load(gitDir); err == nil {
		return nil, nil, errors.New("a bisect session is already in progress; finish it or run 'halfstep reset' first")
	} else if !errors.Is(err, state.ErrNoSession) {
		return nil, nil, err
	}
	bad, err := g.Resolve(badRev)
	if err != nil {
		return nil, nil, err
	}
	if len(goodRevs) == 0 {
		return nil, nil, errors.New("at least one good commit is required")
	}
	goods := make([]string, 0, len(goodRevs))
	for _, rev := range goodRevs {
		sha, err := g.Resolve(rev)
		if err != nil {
			return nil, nil, err
		}
		if sha == bad {
			return nil, nil, fmt.Errorf("%w: %s is both good and bad", ErrContradiction, rev)
		}
		ok, err := g.IsAncestor(sha, bad)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, fmt.Errorf("good commit %s is not an ancestor of the bad commit; halfstep needs good..bad to be a path", rev)
		}
		goods = append(goods, sha)
	}
	if !force {
		dirty, err := g.IsDirty()
		if err != nil {
			return nil, nil, err
		}
		if dirty {
			return nil, nil, fmt.Errorf("%w; commit or stash first (or pass --force)", ErrDirty)
		}
	}
	originalRef, err := g.CurrentRef()
	if err != nil {
		return nil, nil, err
	}
	count, err := g.Count(bad, goods)
	if err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, nil, fmt.Errorf("%w: no commits between good and bad", ErrContradiction)
	}
	st := &state.State{
		OriginalRef:  originalRef,
		InitialBad:   bad,
		InitialGoods: goods,
		InitialCount: count,
		Bad:          bad,
		Goods:        goods,
	}
	e := &Engine{G: g, GitDir: gitDir, St: st}
	p, err := e.advance()
	if err != nil {
		return nil, nil, err
	}
	if err := st.Save(gitDir); err != nil {
		return nil, nil, err
	}
	return e, p, nil
}

// Progress recomputes the snapshot without changing anything.
func (e *Engine) Progress() (*Progress, error) {
	return e.snapshot()
}

// snapshot derives Progress from the current marks.
func (e *Engine) snapshot() (*Progress, error) {
	st := e.St
	count, err := e.G.Count(st.Bad, st.Goods)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("%w: every commit in the range was marked good", ErrContradiction)
	}
	p := &Progress{Count: count, InitialCount: st.InitialCount}
	order, err := e.G.BisectOrder(st.Bad, st.Goods)
	if err != nil {
		return nil, err
	}
	for _, sha := range order {
		if sha != st.Bad && !st.IsSkipped(sha) {
			p.Next = sha
			break
		}
	}
	if p.Next == "" {
		if count == 1 {
			p.Done = true
			p.Culprit = st.Bad
		} else {
			p.Inconclusive = true
			p.Suspects = order // every remaining candidate, best split first
		}
	}
	if err := e.fillRangeFlags(p); err != nil {
		return nil, err
	}
	return p, nil
}

// fillRangeFlags maps the current candidate set onto the initial range so
// the range bar shows where the survivors sit, oldest on the left.
func (e *Engine) fillRangeFlags(p *Progress) error {
	initial, err := e.G.RangeList(e.St.InitialBad, e.St.InitialGoods)
	if err != nil {
		return err
	}
	current, err := e.G.RangeList(e.St.Bad, e.St.Goods)
	if err != nil {
		return err
	}
	live := make(map[string]bool, len(current))
	for _, sha := range current {
		live[sha] = true
	}
	flags := make([]bool, len(initial))
	for i, sha := range initial { // rev-list is newest first; reverse it
		flags[len(initial)-1-i] = live[sha]
	}
	p.RangeFlags = flags
	return nil
}

// advance computes the snapshot and moves HEAD to the next commit to test
// (or to the culprit when the hunt just finished), updating St.Current.
func (e *Engine) advance() (*Progress, error) {
	p, err := e.snapshot()
	if err != nil {
		return nil, err
	}
	switch {
	case p.Next != "":
		if err := e.G.Checkout(p.Next); err != nil {
			return nil, err
		}
		e.St.Current = p.Next
	case p.Done:
		if err := e.G.Checkout(p.Culprit); err != nil {
			return nil, err
		}
		e.St.Current = ""
	default: // inconclusive: stay put, nothing sensible to check out
		e.St.Current = ""
	}
	return p, nil
}

// Mark applies a verdict to rev (or to the commit under test when rev is
// empty), records the step, and advances to the next test commit.
func (e *Engine) Mark(verdict, rev string) (*state.Step, *Progress, error) {
	st := e.St
	sha := st.Current
	if rev != "" {
		resolved, err := e.G.Resolve(rev)
		if err != nil {
			return nil, nil, err
		}
		sha = resolved
	}
	if sha == "" {
		return nil, nil, errors.New("nothing is under test; pass an explicit commit to mark")
	}
	before, err := e.G.Count(st.Bad, st.Goods)
	if err != nil {
		return nil, nil, err
	}
	inRange, err := e.inCandidates(sha)
	if err != nil {
		return nil, nil, err
	}
	// Remember the frontier so a rejected mark leaves the session intact.
	prevBad, prevGoods := st.Bad, st.Goods
	switch verdict {
	case "good":
		if sha == st.Bad {
			return nil, nil, fmt.Errorf("%w: %s is the bad commit", ErrContradiction, sha[:7])
		}
		st.Goods = append(append([]string{}, st.Goods...), sha)
	case "bad":
		if !inRange {
			return nil, nil, fmt.Errorf("commit %s is not in the remaining range", sha[:7])
		}
		st.Bad = sha
	case "skip":
		if !inRange {
			return nil, nil, fmt.Errorf("commit %s is not in the remaining range", sha[:7])
		}
		if sha == st.Bad {
			return nil, nil, errors.New("the bad endpoint is never tested, no need to skip it")
		}
		st.Skipped = append(st.Skipped, sha)
	default:
		return nil, nil, fmt.Errorf("unknown verdict %q", verdict)
	}
	after, err := e.G.Count(st.Bad, st.Goods)
	if err != nil || after == 0 {
		st.Bad, st.Goods = prevBad, prevGoods
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("%w: marking %s good would empty the range", ErrContradiction, sha[:7])
	}
	step := state.Step{
		Commit:  sha,
		Verdict: verdict,
		Before:  before,
		After:   after,
		At:      time.Now().UTC().Format(time.RFC3339),
	}
	st.Steps = append(st.Steps, step)
	p, err := e.advance()
	if err != nil {
		return nil, nil, err
	}
	if err := st.Save(e.GitDir); err != nil {
		return nil, nil, err
	}
	return &step, p, nil
}

// inCandidates reports whether sha is still a possible first-bad commit.
func (e *Engine) inCandidates(sha string) (bool, error) {
	candidates, err := e.G.RangeList(e.St.Bad, e.St.Goods)
	if err != nil {
		return false, err
	}
	for _, c := range candidates {
		if c == sha {
			return true, nil
		}
	}
	return false, nil
}

// Undo removes the most recent verdict by replaying the rest from the
// initial endpoints — the one thing raw `git bisect` cannot do without
// a manual log-edit-replay dance.
func (e *Engine) Undo() (*state.Step, *Progress, error) {
	st := e.St
	if len(st.Steps) == 0 {
		return nil, nil, errors.New("nothing to undo")
	}
	undone := st.Steps[len(st.Steps)-1]
	st.Steps = st.Steps[:len(st.Steps)-1]
	st.Bad = st.InitialBad
	st.Goods = append([]string{}, st.InitialGoods...)
	st.Skipped = nil
	for _, s := range st.Steps {
		switch s.Verdict {
		case "good":
			st.Goods = append(st.Goods, s.Commit)
		case "bad":
			st.Bad = s.Commit
		case "skip":
			st.Skipped = append(st.Skipped, s.Commit)
		}
	}
	p, err := e.advance()
	if err != nil {
		return nil, nil, err
	}
	if err := st.Save(e.GitDir); err != nil {
		return nil, nil, err
	}
	return &undone, p, nil
}

// Reset restores the ref the user was on at start and deletes the session.
// Returns the restored ref. Safe to call twice.
func (e *Engine) Reset() (string, error) {
	ref := e.St.OriginalRef
	if err := e.G.CheckoutRef(ref); err != nil {
		return "", err
	}
	if err := state.Delete(e.GitDir); err != nil {
		return "", err
	}
	return ref, nil
}
