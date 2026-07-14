// Package render turns bisect progress into terminal visuals: the range
// bar (where the survivors sit inside the original range), the shrink
// chart (how each verdict halved the candidate count), and the verdict
// glyphs and colors. Everything here is pure string manipulation with no
// I/O, so it is exhaustively unit-testable.
package render

import (
	"fmt"
	"strings"
)

// Bar cell glyphs. Filled cells still contain candidates; dots have been
// cleared. ASCII-safe alternatives were considered and rejected: the block
// glyphs are what makes the shrinking range legible at a glance.
const (
	cellLive = "█"
	cellDead = "·"
)

// Palette gates ANSI escapes; a disabled palette returns strings verbatim
// so piped output and tests stay byte-stable.
type Palette struct {
	Enabled bool
}

func (p Palette) wrap(code, s string) string {
	if !p.Enabled || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Good renders a "good" verdict (green).
func (p Palette) Good(s string) string { return p.wrap("32", s) }

// Bad renders a "bad" verdict (red).
func (p Palette) Bad(s string) string { return p.wrap("31", s) }

// Warn renders skips and cautions (yellow).
func (p Palette) Warn(s string) string { return p.wrap("33", s) }

// Accent renders commit ids and structural highlights (cyan).
func (p Palette) Accent(s string) string { return p.wrap("36", s) }

// Dim renders secondary detail (bright black).
func (p Palette) Dim(s string) string { return p.wrap("90", s) }

// Bold renders emphasis.
func (p Palette) Bold(s string) string { return p.wrap("1", s) }

// Verdict returns the colored glyph+word for a verdict: ✓ good, ✗ bad,
// ○ skip. Unknown verdicts pass through undecorated.
func (p Palette) Verdict(v string) string {
	switch v {
	case "good":
		return p.Good("✓ good")
	case "bad":
		return p.Bad("✗ bad")
	case "skip":
		return p.Warn("○ skip")
	default:
		return v
	}
}

// Short abbreviates a sha to the conventional 7 characters.
func Short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// Bar compresses the initial range (oldest→newest candidate flags) into a
// fixed-width strip. A cell is live if any commit it covers is still a
// candidate, so narrow survivors never disappear from a wide range. Ranges
// narrower than width shrink the bar instead of stretching it, keeping one
// cell ≥ one commit.
func Bar(flags []bool, width int) string {
	if width <= 0 || len(flags) == 0 {
		return "[]"
	}
	if len(flags) < width {
		width = len(flags)
	}
	var b strings.Builder
	b.WriteString("[")
	for cell := 0; cell < width; cell++ {
		lo := cell * len(flags) / width
		hi := (cell + 1) * len(flags) / width
		live := false
		for i := lo; i < hi; i++ {
			if flags[i] {
				live = true
				break
			}
		}
		if live {
			b.WriteString(cellLive)
		} else {
			b.WriteString(cellDead)
		}
	}
	b.WriteString("]")
	return b.String()
}

// ScaledBar draws count as a solid bar scaled against max (the shrink
// chart rows). Any non-zero count keeps at least one cell so late steps
// with tiny counts stay visible.
func ScaledBar(count, max, width int) string {
	if max <= 0 || width <= 0 || count <= 0 {
		return ""
	}
	n := count * width / max
	if n < 1 {
		n = 1
	}
	if n > width {
		n = width
	}
	return strings.Repeat(cellLive, n)
}

// Plural returns "1 candidate" / "n candidates".
func Plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// StepsLeft phrases the remaining-work estimate: "~4 steps to go",
// "~1 step to go", or "no steps left".
func StepsLeft(n int) string {
	switch {
	case n <= 0:
		return "no steps left"
	case n == 1:
		return "~1 step to go"
	default:
		return fmt.Sprintf("~%d steps to go", n)
	}
}

// RangeLine assembles the canonical two-endpoint bar line:
//
//	good 3c2b1a0 [····████····] f00dfac bad
func RangeLine(pal Palette, goodSha, badSha string, flags []bool, width int) string {
	return fmt.Sprintf("good %s %s %s bad",
		pal.Good(Short(goodSha)), Bar(flags, width), pal.Bad(Short(badSha)))
}
