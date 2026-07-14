// Unit tests for the pure rendering helpers: bar compression, scaled
// shrink-chart rows, palette gating, and the progress phrasing. These are
// the visuals the whole tool exists for, so edge cases (narrow ranges,
// lone survivors in wide ranges) are pinned exactly.
package render

import (
	"strings"
	"testing"
)

func TestBarUniformFill(t *testing.T) {
	if got := Bar([]bool{true, true, true, true}, 4); got != "[████]" {
		t.Fatalf("all live: got %q", got)
	}
	if got := Bar([]bool{false, false, false, false}, 4); got != "[····]" {
		t.Fatalf("all dead: got %q", got)
	}
}

func TestBarDegenerateInputs(t *testing.T) {
	if got := Bar(nil, 40); got != "[]" {
		t.Fatalf("empty flags should render [], got %q", got)
	}
	if got := Bar([]bool{true}, 0); got != "[]" {
		t.Fatalf("zero width should render [], got %q", got)
	}
}

// TestBarNarrowRangeShrinks: 3 commits in a 40-cell request must render 3
// cells, never stretch one commit across many cells.
func TestBarNarrowRangeShrinks(t *testing.T) {
	got := Bar([]bool{false, true, false}, 40)
	if got != "[·█·]" {
		t.Fatalf("got %q", got)
	}
}

// TestBarLoneSurvivorNeverDisappears: compressing 1000 commits into 10
// cells must keep the single live commit visible — a cell is live if ANY
// commit it covers is live.
func TestBarLoneSurvivorNeverDisappears(t *testing.T) {
	flags := make([]bool, 1000)
	flags[537] = true
	got := Bar(flags, 10)
	if strings.Count(got, "█") != 1 {
		t.Fatalf("lone survivor lost: %q", got)
	}
	if got != "[·····█····]" {
		t.Fatalf("survivor in wrong cell: %q", got)
	}
}

// TestBarCompressionCoversAllCommits: cell boundaries must partition the
// range exactly (no commit skipped), checked by lighting each commit in
// turn and requiring some cell to light up.
func TestBarCompressionCoversAllCommits(t *testing.T) {
	const n, width = 97, 13 // deliberately non-divisible
	for i := 0; i < n; i++ {
		flags := make([]bool, n)
		flags[i] = true
		if strings.Count(Bar(flags, width), "█") != 1 {
			t.Fatalf("commit %d not covered by any cell", i)
		}
	}
}

func TestScaledBarMinimumOneCell(t *testing.T) {
	if got := ScaledBar(1, 1000, 24); got != "█" {
		t.Fatalf("non-zero count must keep one cell, got %q", got)
	}
}

func TestScaledBarFullWidth(t *testing.T) {
	if got := ScaledBar(50, 50, 8); got != strings.Repeat("█", 8) {
		t.Fatalf("got %q", got)
	}
}

func TestScaledBarHalf(t *testing.T) {
	if got := ScaledBar(50, 100, 10); got != "█████" {
		t.Fatalf("got %q", got)
	}
}

func TestScaledBarZeroAndDegenerate(t *testing.T) {
	for name, got := range map[string]string{
		"zero count": ScaledBar(0, 10, 10),
		"zero max":   ScaledBar(5, 0, 10),
		"zero width": ScaledBar(5, 10, 0),
	} {
		if got != "" {
			t.Fatalf("%s should render empty, got %q", name, got)
		}
	}
}

// TestScaledBarClampsOvershoot: count > max (possible mid-refactor) must
// not overflow the width.
func TestScaledBarClampsOvershoot(t *testing.T) {
	if got := ScaledBar(200, 100, 10); got != strings.Repeat("█", 10) {
		t.Fatalf("got %q", got)
	}
}

func TestShortSha(t *testing.T) {
	if got := Short("0123456789abcdef"); got != "0123456" {
		t.Fatalf("got %q", got)
	}
	if got := Short("abc"); got != "abc" {
		t.Fatalf("short input must pass through, got %q", got)
	}
}

func TestPaletteDisabledIsVerbatim(t *testing.T) {
	p := Palette{Enabled: false}
	if got := p.Bad("x"); got != "x" {
		t.Fatalf("disabled palette must not emit escapes, got %q", got)
	}
	if got := p.Verdict("good"); got != "✓ good" {
		t.Fatalf("got %q", got)
	}
}

func TestPaletteEnabledWrapsWithReset(t *testing.T) {
	p := Palette{Enabled: true}
	got := p.Good("ok")
	if got != "\x1b[32mok\x1b[0m" {
		t.Fatalf("got %q", got)
	}
	if p.Good("") != "" {
		t.Fatal("empty string must stay empty even when colored")
	}
}

func TestVerdictGlyphs(t *testing.T) {
	p := Palette{Enabled: false}
	cases := map[string]string{"good": "✓ good", "bad": "✗ bad", "skip": "○ skip", "weird": "weird"}
	for in, want := range cases {
		if got := p.Verdict(in); got != want {
			t.Fatalf("Verdict(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlural(t *testing.T) {
	if got := Plural(1, "candidate"); got != "1 candidate" {
		t.Fatalf("got %q", got)
	}
	if got := Plural(0, "step"); got != "0 steps" {
		t.Fatalf("got %q", got)
	}
	if got := Plural(7, "commit"); got != "7 commits" {
		t.Fatalf("got %q", got)
	}
}

func TestStepsLeftPhrasing(t *testing.T) {
	cases := map[int]string{0: "no steps left", 1: "~1 step to go", 6: "~6 steps to go"}
	for in, want := range cases {
		if got := StepsLeft(in); got != want {
			t.Fatalf("StepsLeft(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRangeLineComposition(t *testing.T) {
	p := Palette{Enabled: false}
	got := RangeLine(p, "aaaaaaaaaa", "bbbbbbbbbb", []bool{true, false}, 2)
	if got != "good aaaaaaa [█·] bbbbbbb bad" {
		t.Fatalf("got %q", got)
	}
}
