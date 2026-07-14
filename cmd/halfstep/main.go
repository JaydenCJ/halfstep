// Command halfstep is a guided terminal UI for git bisect: mark commits,
// auto-run a test command, and watch the suspect range shrink step by step.
package main

import (
	"os"

	"github.com/JaydenCJ/halfstep/internal/cli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	os.Exit(cli.Main(os.Args[1:], cli.Env{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Dir:       dir,
		StdoutTTY: isTTY(os.Stdout),
	}))
}

// isTTY reports whether f is a character device (a terminal).
func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
