// run.go implements `halfstep run -- <command>`: the fully automated hunt.
// The command is executed once per candidate checkout and its exit code is
// translated into a verdict with `git bisect run` semantics, so any script
// written for git bisect drives halfstep unchanged.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/JaydenCJ/halfstep/internal/autorun"
	"github.com/JaydenCJ/halfstep/internal/render"
)

func cmdRun(args []string, env Env) int {
	fs := newFlagSet("run", env)
	colorMode := fs.String("color", "auto", "color output: auto, always, or never")
	width := fs.Int("width", 40, "range bar width in cells")
	verbose := fs.Bool("verbose", false, "stream the test command's output to stderr")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: halfstep run [flags] -- <command ...>")
		fs.PrintDefaults()
	}
	// Split at the -- separator ourselves so command flags survive intact.
	ownArgs, command, found := splitDashDash(args)
	if !found {
		if code, ok := parseFlags(fs, args); !ok {
			return code // -h/--help or a malformed flag; usage was printed
		}
		fmt.Fprintln(env.Stderr, "halfstep: run needs '--' before the test command (halfstep run -- ./test.sh)")
		return exitUsage
	}
	if code, ok := parseFlags(fs, ownArgs); !ok {
		return code
	}
	if len(command) == 0 {
		fmt.Fprintln(env.Stderr, "halfstep: no test command after '--'")
		return exitUsage
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
	p, err := e.Progress()
	if err != nil {
		return fail(env, err)
	}
	for p.Next != "" {
		info, err := e.G.Info(p.Next)
		if err != nil {
			return fail(env, err)
		}
		code, runErr := runCommand(command, env, *verbose)
		if runErr != nil {
			fmt.Fprintf(env.Stderr, "halfstep: cannot run %q: %v\n", command[0], runErr)
			return exitRuntime
		}
		verdict, abortErr := autorun.Classify(code)
		if abortErr != nil {
			fmt.Fprintf(env.Stderr, "halfstep: %v\n", abortErr)
			fmt.Fprintln(env.Stderr, "the session is untouched; fix the test command and rerun")
			return exitRuntime
		}
		step, next, err := e.Mark(verdict, "")
		if err != nil {
			return fail(env, err)
		}
		// Pad the subject before coloring; ANSI escapes defeat %-32s.
		subject := fmt.Sprintf("%-32s", ellipsis(info.Subject, 32))
		fmt.Fprintf(env.Stdout, "step %-2d %s %s %s exit %-3d · %3d → %-3d %s\n",
			len(e.St.Steps), verdictCell(pal, step.Verdict), pal.Accent(info.Short),
			pal.Dim(subject), code, step.Before, step.After,
			render.Bar(next.RangeFlags, *width))
		p = next
	}
	// Next is empty, so the snapshot is either Done or Inconclusive.
	if p.Done {
		if err := culpritBlock(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
		return exitOK
	}
	if err := suspectsBlock(env.Stdout, pal, e, p); err != nil {
		return fail(env, err)
	}
	return exitBisect
}

// runCommand executes the test command in the repository directory and
// returns its exit code. A start failure (binary not found) is an error,
// not a verdict.
func runCommand(command []string, env Env, verbose bool) (int, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = env.Dir
	if verbose {
		cmd.Stdout = env.Stderr
		cmd.Stderr = env.Stderr
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

// splitDashDash separates halfstep's own flags from the test command.
func splitDashDash(args []string) (own, command []string, found bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}

// ellipsis truncates s to max runes with a … marker.
func ellipsis(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
