// start.go implements `halfstep start`: the wizard that turns "I know it
// worked once and it's broken now" into a running bisect session. Missing
// endpoints are prompted for interactively, so nobody has to remember the
// git bisect incantation order.
package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/JaydenCJ/halfstep/internal/engine"
	"github.com/JaydenCJ/halfstep/internal/gitx"
	"github.com/JaydenCJ/halfstep/internal/render"
)

func cmdStart(args []string, env Env) int {
	fs := newFlagSet("start", env)
	bad := fs.String("bad", "", "revision known to be broken (default: prompt, suggesting HEAD)")
	var goods multiFlag
	fs.Var(&goods, "good", "revision known to work (repeatable; prompted for when absent)")
	force := fs.Bool("force", false, "start even with uncommitted changes to tracked files")
	colorMode := fs.String("color", "auto", "color output: auto, always, or never")
	width := fs.Int("width", 40, "range bar width in cells")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(env.Stderr, "halfstep: start takes no positional arguments (got %q); use --bad/--good\n", fs.Arg(0))
		return exitUsage
	}
	pal, err := palette(*colorMode, env)
	if err != nil {
		fmt.Fprintf(env.Stderr, "halfstep: %v\n", err)
		return exitUsage
	}

	// The wizard part: ask for whatever was not given on the command line.
	reader := bufio.NewReader(env.Stdin)
	if *bad == "" {
		v, err := prompt(reader, env, "Bad commit — one where the problem happens [HEAD]: ")
		if err != nil {
			return fail(env, err)
		}
		if v == "" {
			v = "HEAD"
		}
		*bad = v
	}
	if len(goods) == 0 {
		v, err := prompt(reader, env, "Good commit — one from before the problem (tag, sha, HEAD~n): ")
		if err != nil {
			return fail(env, err)
		}
		if v == "" {
			fmt.Fprintln(env.Stderr, "halfstep: a good commit is required — without one there is no range to search")
			return exitUsage
		}
		goods = append(goods, v)
	}

	e, p, err := engine.Start(gitx.Git{Dir: env.Dir}, *bad, goods, *force)
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "halfstep: hunting the first bad commit in %s (%s)\n\n",
		pal.Accent(render.Short(oldestGood(e.St))+".."+render.Short(e.St.InitialBad)),
		render.Plural(p.InitialCount, "commit"))
	rangeBlock(env.Stdout, pal, e, p, *width)
	if p.Done {
		if err := culpritBlock(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
		return exitOK
	}
	if err := nextLine(env.Stdout, pal, e, p); err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "  test it, then: halfstep good | halfstep bad | halfstep skip\n")
	fmt.Fprintf(env.Stdout, "  or hand the wheel over: halfstep run -- <your test command>\n")
	return exitOK
}

// prompt writes the question to stdout and reads one trimmed line.
func prompt(r *bufio.Reader, env Env, question string) (string, error) {
	fmt.Fprint(env.Stdout, question)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("no answer given (stdin closed); pass --bad/--good instead")
	}
	return strings.TrimSpace(line), nil
}
