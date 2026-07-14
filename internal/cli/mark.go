// mark.go implements the verdict commands — `halfstep good|bad|skip` — and
// `halfstep undo`. Every verdict prints the shrinking range so the payoff
// of each test is visible immediately.
package cli

import (
	"fmt"

	"github.com/JaydenCJ/halfstep/internal/engine"
	"github.com/JaydenCJ/halfstep/internal/render"
	"github.com/JaydenCJ/halfstep/internal/state"
)

// cmdMark handles good, bad, and skip; verdict is the subcommand name.
func cmdMark(verdict string, args []string, env Env) int {
	fs := newFlagSet(verdict, env)
	colorMode := fs.String("color", "auto", "color output: auto, always, or never")
	width := fs.Int("width", 40, "range bar width in cells")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() > 1 {
		fmt.Fprintf(env.Stderr, "halfstep: %s takes at most one revision\n", verdict)
		return exitUsage
	}
	rev := ""
	if fs.NArg() == 1 {
		rev = fs.Arg(0)
	}
	pal, err := palette(*colorMode, env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "halfstep: %v\n", err)
		return exitUsage
	}
	e, err := openEngine(env)
	if err != nil {
		return fail(env, err)
	}
	step, p, err := e.Mark(verdict, rev)
	if err != nil {
		return fail(env, err)
	}
	return reportStep(env, pal, e, step, p, *width)
}

func cmdUndo(args []string, env Env) int {
	fs := newFlagSet("undo", env)
	colorMode := fs.String("color", "auto", "color output: auto, always, or never")
	width := fs.Int("width", 40, "range bar width in cells")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	pal, err := palette(*colorMode, env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "halfstep: %v\n", err)
		return exitUsage
	}
	e, err := openEngine(env)
	if err != nil {
		return fail(env, err)
	}
	undone, p, err := e.Undo()
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "undid %s on %s — back to %s\n\n",
		pal.Verdict(undone.Verdict), pal.Accent(render.Short(undone.Commit)),
		render.Plural(p.Count, "candidate"))
	rangeBlock(env.Stdout, pal, e, p, *width)
	if p.Next != "" {
		if err := nextLine(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
	}
	return exitOK
}

// reportStep prints the outcome of one verdict: the delta line, the bar,
// and either the next commit to test, the culprit, or the suspects.
func reportStep(env Env, pal render.Palette, e *engine.Engine, step *state.Step, p *engine.Progress, width int) int {
	fmt.Fprintf(env.Stdout, "%s %s — %d → %s\n\n",
		pal.Verdict(step.Verdict), pal.Accent(render.Short(step.Commit)),
		step.Before, render.Plural(step.After, "candidate"))
	rangeBlock(env.Stdout, pal, e, p, width)
	switch {
	case p.Done:
		if err := culpritBlock(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
		return exitOK
	case p.Inconclusive:
		if err := suspectsBlock(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
		return exitBisect
	default:
		if err := nextLine(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
		return exitOK
	}
}
