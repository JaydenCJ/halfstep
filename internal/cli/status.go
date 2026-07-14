// status.go implements `halfstep status` (where the hunt stands, in text
// or stable JSON) and `halfstep log` (the verdict history rendered as a
// shrink chart — each row's bar is scaled to the candidates that remained
// after that verdict, so the halving is visible as a staircase).
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/JaydenCJ/halfstep/internal/engine"
	"github.com/JaydenCJ/halfstep/internal/render"
	"github.com/JaydenCJ/halfstep/internal/state"
	"github.com/JaydenCJ/halfstep/internal/version"
)

func cmdStatus(args []string, env Env) int {
	fs := newFlagSet("status", env)
	colorMode := fs.String("color", "auto", "color output: auto, always, or never")
	width := fs.Int("width", 40, "range bar width in cells")
	format := fs.String("format", "text", "output format: text or json")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintf(env.Stderr, "halfstep: bad --format %q (want text or json)\n", *format)
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
	if *format == "json" {
		return statusJSON(env, e, p)
	}
	fmt.Fprintf(env.Stdout, "halfstep: hunting between %s (good) and %s (bad)\n\n",
		pal.Good(render.Short(oldestGood(e.St))), pal.Bad(render.Short(e.St.InitialBad)))
	rangeBlock(env.Stdout, pal, e, p, *width)
	switch {
	case p.Done:
		if err := culpritBlock(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
	case p.Inconclusive:
		if err := suspectsBlock(env.Stdout, pal, e, p); err != nil {
			return fail(env, err)
		}
	default:
		info, err := e.G.Info(p.Next)
		if err != nil {
			return fail(env, err)
		}
		fmt.Fprintf(env.Stdout, "  under test: %s %s · %s so far\n",
			pal.Accent(info.Short), quoted(info.Subject),
			render.Plural(len(e.St.Steps), "verdict"))
		fmt.Fprintf(env.Stdout, "\n  mark it: halfstep good | halfstep bad | halfstep skip · or: halfstep run -- <cmd>\n")
	}
	return exitOK
}

// jsonStatus is the stable machine-readable envelope; additions are
// backwards compatible, removals bump schema_version.
type jsonStatus struct {
	Tool          string       `json:"tool"`
	SchemaVersion int          `json:"schema_version"`
	Version       string       `json:"version"`
	OriginalRef   string       `json:"original_ref"`
	InitialBad    string       `json:"initial_bad"`
	InitialGoods  []string     `json:"initial_goods"`
	InitialCount  int          `json:"initial_count"`
	Bad           string       `json:"bad"`
	Goods         []string     `json:"goods"`
	Skipped       []string     `json:"skipped"`
	Candidates    int          `json:"candidates"`
	StepsLeft     int          `json:"steps_left_estimate"`
	UnderTest     string       `json:"under_test,omitempty"`
	Done          bool         `json:"done"`
	Culprit       string       `json:"culprit,omitempty"`
	Inconclusive  bool         `json:"inconclusive"`
	Suspects      []string     `json:"suspects,omitempty"`
	Steps         []state.Step `json:"steps"`
}

func statusJSON(env Env, e *engine.Engine, p *engine.Progress) int {
	st := e.St
	doc := jsonStatus{
		Tool:          "halfstep",
		SchemaVersion: state.SchemaVersion,
		Version:       version.Version,
		OriginalRef:   st.OriginalRef,
		InitialBad:    st.InitialBad,
		InitialGoods:  st.InitialGoods,
		InitialCount:  st.InitialCount,
		Bad:           st.Bad,
		Goods:         st.Goods,
		Skipped:       emptyNotNull(st.Skipped),
		Candidates:    p.Count,
		StepsLeft:     p.StepsLeftEstimate(),
		UnderTest:     st.Current,
		Done:          p.Done,
		Culprit:       p.Culprit,
		Inconclusive:  p.Inconclusive,
		Suspects:      p.Suspects,
		Steps:         emptyNotNullSteps(st.Steps),
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fail(env, err)
	}
	fmt.Fprintln(env.Stdout, string(raw))
	return exitOK
}

// emptyNotNull keeps JSON arrays as [] instead of null for consumers.
func emptyNotNull(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func emptyNotNullSteps(s []state.Step) []state.Step {
	if s == nil {
		return []state.Step{}
	}
	return s
}

func cmdLog(args []string, env Env) int {
	fs := newFlagSet("log", env)
	colorMode := fs.String("color", "auto", "color output: auto, always, or never")
	width := fs.Int("width", 24, "shrink chart width in cells")
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
	p, err := e.Progress()
	if err != nil {
		return fail(env, err)
	}
	st := e.St
	fmt.Fprintf(env.Stdout, "halfstep log — %s, %d → %s\n\n",
		render.Plural(len(st.Steps), "verdict"), st.InitialCount,
		render.Plural(p.Count, "candidate"))
	fmt.Fprintf(env.Stdout, "  step  verdict  commit   candidates\n")
	fmt.Fprintf(env.Stdout, "     0  %s   %s  %9d  %s %s\n",
		pal.Dim("start"), render.Short(st.InitialBad), st.InitialCount,
		render.ScaledBar(st.InitialCount, st.InitialCount, *width),
		pal.Dim("(good.."+render.Short(st.InitialBad)+")"))
	for i, s := range st.Steps {
		info, err := e.G.Info(s.Commit)
		if err != nil {
			return fail(env, err)
		}
		fmt.Fprintf(env.Stdout, "  %4d  %s  %s  %4d → %-3d %s %s\n",
			i+1, verdictCell(pal, s.Verdict), render.Short(s.Commit),
			s.Before, s.After,
			render.ScaledBar(s.After, st.InitialCount, *width),
			pal.Dim(ellipsis(info.Subject, 40)))
	}
	if p.Done {
		fmt.Fprintf(env.Stdout, "\n  first bad commit: %s\n", pal.Bad(render.Short(p.Culprit)))
	}
	return exitOK
}

// verdictCell pads the verdict to a fixed 7-cell column before coloring,
// because ANSI escapes would defeat %-7s alignment.
func verdictCell(pal render.Palette, v string) string {
	plain := pal.Verdict(v)
	// Pad based on the visible glyph+word length: "✓ good" and "○ skip"
	// show 6 cells, "✗ bad" shows 5.
	pad := ""
	if v == "bad" {
		pad = "  "
	} else {
		pad = " "
	}
	return plain + pad
}
