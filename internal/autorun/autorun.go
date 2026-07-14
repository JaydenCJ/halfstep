// Package autorun maps a test command's exit status to a bisect verdict,
// using the exact contract `git bisect run` established so existing test
// scripts work unmodified: 0 is good, 125 is skip, other codes below 128
// are bad, and 128+ (usually a signal) aborts the whole hunt.
package autorun

import "fmt"

// Verdicts returned by Classify.
const (
	VerdictGood = "good"
	VerdictBad  = "bad"
	VerdictSkip = "skip"
)

// Classify turns an exit code into a verdict. The error return marks an
// abort: the script itself is broken (or was killed), so no verdict about
// the commit can be trusted.
func Classify(code int) (string, error) {
	switch {
	case code == 0:
		return VerdictGood, nil
	case code == 125:
		return VerdictSkip, nil
	case code > 0 && code < 128:
		return VerdictBad, nil
	default:
		return "", fmt.Errorf("test command exited %d (outside 0-127); aborting the run", code)
	}
}
