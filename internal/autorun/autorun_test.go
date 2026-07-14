// Tests pinning the exit-code contract to `git bisect run` semantics —
// the whole point of the run command is that scripts written for git
// bisect keep working unchanged.
package autorun

import "testing"

func TestExitZeroIsGood(t *testing.T) {
	v, err := Classify(0)
	if err != nil || v != VerdictGood {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestExit125IsSkip(t *testing.T) {
	v, err := Classify(125)
	if err != nil || v != VerdictSkip {
		t.Fatalf("got %q, %v", v, err)
	}
}

func TestOrdinaryFailuresAreBad(t *testing.T) {
	// 1 (test failed), 2 (usage error in the script), 124 (timeout(1)),
	// 126/127 (not executable / not found in a subshell) are all "bad" in
	// the git bisect run contract.
	for _, code := range []int{1, 2, 124, 126, 127} {
		v, err := Classify(code)
		if err != nil || v != VerdictBad {
			t.Fatalf("Classify(%d) = %q, %v; want bad", code, v, err)
		}
	}
}

func TestSignalAndHighCodesAbort(t *testing.T) {
	// 128+ means the script itself blew up (usually a signal); trusting
	// such an exit as a verdict would send the hunt in a random direction.
	for _, code := range []int{128, 130, 137, 255} {
		if _, err := Classify(code); err == nil {
			t.Fatalf("Classify(%d) should abort", code)
		}
	}
}

func TestNegativeCodeAborts(t *testing.T) {
	// exec.ExitError reports -1 for a signal-killed process on some paths.
	if _, err := Classify(-1); err == nil {
		t.Fatal("Classify(-1) should abort")
	}
}
