// Package cli wires the halfstep subcommands together. Main takes argv and
// an Env of writers and returns an exit code, so the full CLI runs
// in-process in tests. Exit codes: 0 ok, 1 bisect problem (no session,
// contradiction, inconclusive hunt), 2 usage error, 3 git or runtime error.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/JaydenCJ/halfstep/internal/engine"
	"github.com/JaydenCJ/halfstep/internal/gitx"
	"github.com/JaydenCJ/halfstep/internal/render"
	"github.com/JaydenCJ/halfstep/internal/state"
	"github.com/JaydenCJ/halfstep/internal/version"
)

// Exit codes shared by every subcommand.
const (
	exitOK      = 0
	exitBisect  = 1 // no session, contradiction, inconclusive, refused mark
	exitUsage   = 2
	exitRuntime = 3 // git failed, test command aborted, unexpected I/O
)

const usageText = `halfstep — a guided terminal UI for git bisect: mark, auto-run, and watch the range shrink.

Usage:
  halfstep [-C <dir>] start [--bad rev] [--good rev]...  begin a hunt (prompts for missing endpoints)
  halfstep [-C <dir>] good|bad|skip [rev]                mark the commit under test (or rev) and advance
  halfstep [-C <dir>] undo                               take back the most recent verdict
  halfstep [-C <dir>] run [flags] -- <command ...>       let a test command drive the whole hunt
  halfstep [-C <dir>] status [--format text|json]        show the range bar and what to do next
  halfstep [-C <dir>] log                                verdict history as a shrink chart
  halfstep [-C <dir>] reset                              return to the original branch and clear state
  halfstep version                                       print the version

Auto-run verdicts (the 'git bisect run' contract): exit 0 = good, 125 = skip,
1-127 = bad, 128+ aborts the hunt.

Exit codes: 0 ok · 1 bisect problem · 2 usage error · 3 git or runtime error.
Run 'halfstep <command> -h' for the flags of each command.
`

// Env carries process-level context Main should not reach for globally.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Dir is the working directory git commands run in (set by -C).
	Dir string
	// StdoutTTY: whether stdout is a terminal; drives --color auto.
	StdoutTTY bool
}

// Main dispatches argv (without the program name) and returns the exit code.
func Main(argv []string, env Env) int {
	// Global -C <dir>, git-style, before the subcommand.
	for len(argv) > 0 && (argv[0] == "-C" || argv[0] == "--chdir") {
		if len(argv) < 2 {
			fmt.Fprintln(env.Stderr, "halfstep: -C needs a directory argument")
			return exitUsage
		}
		env.Dir = argv[1]
		argv = argv[2:]
	}
	if len(argv) == 0 {
		fmt.Fprint(env.Stderr, usageText)
		return exitUsage
	}
	cmd, rest := argv[0], argv[1:]
	switch cmd {
	case "start":
		return cmdStart(rest, env)
	case "good", "bad", "skip":
		return cmdMark(cmd, rest, env)
	case "undo":
		return cmdUndo(rest, env)
	case "run":
		return cmdRun(rest, env)
	case "status":
		return cmdStatus(rest, env)
	case "log":
		return cmdLog(rest, env)
	case "reset":
		return cmdReset(rest, env)
	case "version", "--version", "-V":
		fmt.Fprintf(env.Stdout, "halfstep %s\n", version.Version)
		return exitOK
	case "help", "--help", "-h":
		fmt.Fprint(env.Stdout, usageText)
		return exitOK
	default:
		fmt.Fprintf(env.Stderr, "halfstep: unknown command %q\n\n", cmd)
		fmt.Fprint(env.Stderr, usageText)
		return exitUsage
	}
}

// multiFlag is a repeatable string flag (e.g. --good v1.0 --good v1.1).
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

// newFlagSet builds a FlagSet that reports usage errors on env.Stderr.
func newFlagSet(name string, env Env) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	return fs
}

// parseFlags runs fs.Parse and maps the outcome to an exit code: ok means
// keep going; -h/--help exits 0 (the flag package already printed the
// usage), any real parse error exits 2.
func parseFlags(fs *flag.FlagSet, args []string) (code int, ok bool) {
	switch err := fs.Parse(args); {
	case err == nil:
		return exitOK, true
	case errors.Is(err, flag.ErrHelp):
		return exitOK, false
	default:
		return exitUsage, false
	}
}

// palette resolves a --color mode against TTY detection.
func palette(mode string, env Env) (render.Palette, error) {
	switch mode {
	case "auto":
		return render.Palette{Enabled: env.StdoutTTY}, nil
	case "always":
		return render.Palette{Enabled: true}, nil
	case "never":
		return render.Palette{Enabled: false}, nil
	default:
		return render.Palette{}, fmt.Errorf("bad --color %q (want auto, always, or never)", mode)
	}
}

// fail prints err with the halfstep: prefix and maps it to an exit code:
// git subprocess failures are runtime errors (3), everything the user can
// correct with a different command is a bisect problem (1).
func fail(env Env, err error) int {
	if errors.Is(err, state.ErrNoSession) {
		fmt.Fprintln(env.Stderr, "halfstep: no bisect session in progress (start one with 'halfstep start')")
		return exitBisect
	}
	fmt.Fprintf(env.Stderr, "halfstep: %v\n", err)
	var ge *gitx.Error
	if errors.As(err, &ge) {
		return exitRuntime
	}
	return exitBisect
}

// openEngine loads the current session for env.Dir.
func openEngine(env Env) (*engine.Engine, error) {
	return engine.Open(gitx.Git{Dir: env.Dir})
}

// rangeBlock prints the two canonical progress lines:
//
//	good 3c2b1a0 [····████████····] f00dfac bad
//	48 of 96 candidates · ~6 steps to go
func rangeBlock(w io.Writer, pal render.Palette, e *engine.Engine, p *engine.Progress, width int) {
	fmt.Fprintf(w, "  %s\n", render.RangeLine(pal, oldestGood(e.St), e.St.InitialBad, p.RangeFlags, width))
	counts := render.Plural(p.Count, "candidate")
	if p.Count != p.InitialCount {
		counts = fmt.Sprintf("%d of %s", p.Count, render.Plural(p.InitialCount, "candidate"))
	}
	fmt.Fprintf(w, "  %s · %s\n", counts, pal.Dim(render.StepsLeft(p.StepsLeftEstimate())))
}

// oldestGood picks the display sha for the good end of the bar: the first
// good endpoint given at start (multiple goods share one end).
func oldestGood(st *state.State) string {
	if len(st.InitialGoods) > 0 {
		return st.InitialGoods[0]
	}
	return ""
}

// nextLine prints what was just checked out for testing.
func nextLine(w io.Writer, pal render.Palette, e *engine.Engine, p *engine.Progress) error {
	info, err := e.G.Info(p.Next)
	if err != nil {
		return err
	}
	step := len(e.St.Steps) + 1
	fmt.Fprintf(w, "\n→ checked out %s %s %s\n",
		pal.Accent(info.Short), quoted(info.Subject), pal.Dim(fmt.Sprintf("(step %d)", step)))
	return nil
}

// culpritBlock prints the final verdict box once one candidate remains.
func culpritBlock(w io.Writer, pal render.Palette, e *engine.Engine, p *engine.Progress) error {
	info, err := e.G.Info(p.Culprit)
	if err != nil {
		return err
	}
	rule := strings.Repeat("─", 46)
	fmt.Fprintf(w, "\n┌%s\n", rule)
	fmt.Fprintf(w, "│ %s %s\n", pal.Bold("first bad commit:"), pal.Bad(info.Short))
	fmt.Fprintf(w, "│ author : %s\n", info.Author)
	fmt.Fprintf(w, "│ date   : %s\n", info.Date)
	fmt.Fprintf(w, "│ subject: %s\n", info.Subject)
	fmt.Fprintf(w, "└%s\n", rule)
	fmt.Fprintf(w, "  found in %s · %d candidates narrowed to 1\n",
		render.Plural(len(e.St.Steps), "step"), p.InitialCount)
	fmt.Fprintf(w, "  HEAD is on the culprit — 'halfstep reset' returns to %s\n", e.St.OriginalRef)
	return nil
}

// suspectsBlock prints the honest answer when skips block an exact verdict.
func suspectsBlock(w io.Writer, pal render.Palette, e *engine.Engine, p *engine.Progress) error {
	fmt.Fprintf(w, "\n%s only untestable commits remain; the first bad commit is one of:\n",
		pal.Warn("inconclusive:"))
	for _, sha := range p.Suspects {
		info, err := e.G.Info(sha)
		if err != nil {
			return err
		}
		marker := ""
		if e.St.IsSkipped(sha) {
			marker = pal.Warn(" (skipped)")
		}
		fmt.Fprintf(w, "  %s %s%s\n", pal.Accent(info.Short), info.Subject, marker)
	}
	fmt.Fprintln(w, "  make one of them buildable and mark it, or 'halfstep reset' to stop here")
	return nil
}

// quoted wraps a commit subject for prose lines.
func quoted(s string) string { return "\"" + s + "\"" }
