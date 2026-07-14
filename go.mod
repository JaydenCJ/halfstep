// halfstep — a guided terminal UI for git bisect: mark, auto-run, and
// visualize the shrinking range.
//
// version:    0.1.0
// author:     JaydenCJ
// license:    MIT
// repository: https://github.com/JaydenCJ/halfstep
// keywords:   git, bisect, debugging, regression, tui, cli, developer-tools
//
// Zero runtime dependencies: standard library only (git itself is the
// single external tool, invoked as a subprocess).
module github.com/JaydenCJ/halfstep

go 1.22
