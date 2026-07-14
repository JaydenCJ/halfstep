// reset.go implements `halfstep reset`: put HEAD back where the user was
// when the hunt began and forget the session. Idempotent — resetting with
// no session in progress is a no-op that still exits 0, so cleanup scripts
// can call it unconditionally.
package cli

import (
	"errors"
	"fmt"

	"github.com/JaydenCJ/halfstep/internal/state"
)

func cmdReset(args []string, env Env) int {
	fs := newFlagSet("reset", env)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(env.Stderr, "halfstep: reset takes no arguments")
		return exitUsage
	}
	e, err := openEngine(env)
	if err != nil {
		if errors.Is(err, state.ErrNoSession) {
			fmt.Fprintln(env.Stdout, "nothing to reset — no bisect session in progress")
			return exitOK
		}
		return fail(env, err)
	}
	ref, err := e.Reset()
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintf(env.Stdout, "session cleared — back on %s\n", ref)
	return exitOK
}
