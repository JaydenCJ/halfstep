// In-process integration tests for the CLI: real git repositories built in
// t.TempDir() via deterministic fast-import streams, the full command
// surface driven through Main with captured stdout/stderr, and every exit
// code pinned. No binaries are built, nothing sleeps, nothing networks.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// gitRun executes git in dir with a pinned identity/date environment.
func gitRun(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Dev Example", "GIT_AUTHOR_EMAIL=dev@example.test",
		"GIT_COMMITTER_NAME=Dev Example", "GIT_COMMITTER_EMAIL=dev@example.test",
	)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// bugRepo builds n linear commits on main where commits >= bugAt (1-based)
// write "BUG" into lib.txt. Returns the repo dir and shas oldest-first.
func bugRepo(t *testing.T, n, bugAt int) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "", "init", "-q", "-b", "main")
	var b strings.Builder
	for i := 1; i <= n; i++ {
		content := fmt.Sprintf("v%d", i)
		if bugAt > 0 && i >= bugAt {
			content = fmt.Sprintf("v%d BUG", i)
		}
		fmt.Fprintf(&b, "blob\nmark :%d\ndata %d\n%s\n", i*2-1, len(content), content)
		fmt.Fprintf(&b, "commit refs/heads/main\nmark :%d\n", i*2)
		when := fmt.Sprintf("%d +0000", 1774000000+i*60)
		fmt.Fprintf(&b, "author Dev Example <dev@example.test> %s\n", when)
		fmt.Fprintf(&b, "committer Dev Example <dev@example.test> %s\n", when)
		fmt.Fprintf(&b, "data %d\nchange %d\n", len(fmt.Sprintf("change %d", i)), i)
		fmt.Fprintf(&b, "M 100644 :%d lib.txt\n\n", i*2-1)
	}
	gitRun(t, dir, b.String(), "fast-import", "--quiet")
	gitRun(t, dir, "", "reset", "--hard", "-q", "main")
	shas := strings.Fields(gitRun(t, dir, "", "rev-list", "--reverse", "HEAD"))
	if len(shas) != n {
		t.Fatalf("built %d commits, want %d", len(shas), n)
	}
	return dir, shas
}

// execCLI runs Main in-process and returns (exit, stdout, stderr).
func execCLI(t *testing.T, dir, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Main(args, Env{
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errBuf,
		Dir:    dir,
	})
	return code, out.String(), errBuf.String()
}

// startSession begins a hunt over the whole history with colors off.
func startSession(t *testing.T, dir string, shas []string) string {
	t.Helper()
	code, out, errOut := execCLI(t, dir, "", "start", "--color", "never",
		"--bad", "HEAD", "--good", shas[0])
	if code != 0 {
		t.Fatalf("start: exit %d\n%s%s", code, out, errOut)
	}
	return out
}

func TestVersionAndHelpExitZero(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-V"}} {
		code, out, _ := execCLI(t, t.TempDir(), "", args...)
		if code != 0 || out != "halfstep 0.1.0\n" {
			t.Fatalf("%v: exit %d, out %q", args, code, out)
		}
	}
	code, out, _ := execCLI(t, t.TempDir(), "", "help")
	if code != 0 || !strings.Contains(out, "halfstep [-C <dir>] start") {
		t.Fatalf("help: exit %d, out %q", code, out)
	}
}

// TestUsageErrorsExit2 pins every user-mistake path to exit code 2 with an
// actionable message.
func TestUsageErrorsExit2(t *testing.T) {
	dir, _ := bugRepo(t, 3, 0)
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no args", nil, "Usage:"},
		{"unknown command", []string{"frobnicate"}, "unknown command"},
		{"-C without dir", []string{"-C"}, "needs a directory"},
		{"start positional", []string{"start", "HEAD"}, "no positional arguments"},
		{"start bad color", []string{"start", "--color", "sometimes", "--bad", "HEAD", "--good", "HEAD~1"}, "bad --color"},
		{"mark two revs", []string{"good", "a", "b"}, "at most one revision"},
		{"run without --", []string{"run", "true"}, "needs '--'"},
		{"run empty command", []string{"run", "--"}, "no test command"},
		{"status bad format", []string{"status", "--format", "yaml"}, "bad --format"},
		{"reset with args", []string{"reset", "now"}, "no arguments"},
	}
	for _, c := range cases {
		code, _, errOut := execCLI(t, dir, "", c.args...)
		if code != exitUsage {
			t.Fatalf("%s: exit %d, want 2 (stderr %q)", c.name, code, errOut)
		}
		if !strings.Contains(errOut, c.wantErr) {
			t.Fatalf("%s: stderr %q missing %q", c.name, errOut, c.wantErr)
		}
	}
}

// TestHelpFlagExitsZero: -h/--help on a subcommand prints its usage and
// exits 0, not 2 — including `run -h`, which has no '--' separator and
// must not be mistaken for a missing test command.
func TestHelpFlagExitsZero(t *testing.T) {
	for _, args := range [][]string{
		{"start", "-h"}, {"run", "-h"}, {"run", "-h", "--", "true"}, {"status", "--help"},
	} {
		code, _, errOut := execCLI(t, t.TempDir(), "", args...)
		if code != exitOK {
			t.Fatalf("%v: exit %d, want 0 (stderr %q)", args, code, errOut)
		}
		if !strings.Contains(errOut, "Usage") {
			t.Fatalf("%v: usage not printed, stderr %q", args, errOut)
		}
	}
}

func TestCommandsWithoutSessionExit1(t *testing.T) {
	dir, _ := bugRepo(t, 3, 0)
	for _, args := range [][]string{
		{"status"}, {"log"}, {"good"}, {"bad"}, {"skip"}, {"undo"},
		{"run", "--", "true"},
	} {
		code, _, errOut := execCLI(t, dir, "", args...)
		if code != exitBisect || !strings.Contains(errOut, "no bisect session") {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
}

func TestOutsideRepoExit3(t *testing.T) {
	code, _, errOut := execCLI(t, t.TempDir(), "", "status")
	if code != exitRuntime {
		t.Fatalf("exit %d, want 3 (stderr %q)", code, errOut)
	}
}

func TestStartShowsRangeBarAndNextStep(t *testing.T) {
	dir, shas := bugRepo(t, 20, 0)
	out := startSession(t, dir, shas)
	for _, want := range []string{
		"hunting the first bad commit",
		"(19 commits)",
		"good " + shas[0][:7],
		shas[19][:7] + " bad",
		"19 candidates · ~5 steps to go",
		"→ checked out",
		"(step 1)",
		"halfstep good | halfstep bad | halfstep skip",
		"halfstep run --",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("start output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatal("--color never must not emit ANSI escapes")
	}
}

func TestStartWizardPromptsForMissingEndpoints(t *testing.T) {
	dir, shas := bugRepo(t, 10, 0)
	// Empty first answer accepts the HEAD default; second gives the good.
	code, out, errOut := execCLI(t, dir, "\n"+shas[0]+"\n", "start", "--color", "never")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "Bad commit — one where the problem happens [HEAD]: ") {
		t.Fatalf("bad prompt missing:\n%s", out)
	}
	if !strings.Contains(out, "Good commit — one from before the problem") {
		t.Fatalf("good prompt missing:\n%s", out)
	}
	if !strings.Contains(out, "9 candidates") {
		t.Fatalf("wizard should have started a 9-candidate hunt:\n%s", out)
	}
}

func TestStartWizardEmptyGoodIsUsageError(t *testing.T) {
	dir, _ := bugRepo(t, 5, 0)
	code, _, errOut := execCLI(t, dir, "\n\n", "start", "--color", "never")
	if code != exitUsage || !strings.Contains(errOut, "good commit is required") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestStartUnresolvableRevExit1(t *testing.T) {
	dir, _ := bugRepo(t, 5, 0)
	code, _, errOut := execCLI(t, dir, "", "start", "--bad", "HEAD", "--good", "v9.9.9")
	if code != exitBisect || !strings.Contains(errOut, "cannot resolve") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestStartDirtyTreeExit1MentionsForce(t *testing.T) {
	dir, shas := bugRepo(t, 5, 0)
	if err := os.WriteFile(dir+"/lib.txt", []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := execCLI(t, dir, "", "start", "--bad", "HEAD", "--good", shas[0])
	if code != exitBisect || !strings.Contains(errOut, "--force") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

// TestManualMarkFlowFindsCulprit walks a whole hunt by hand: mark the
// worktree verdict after each checkout and end at the planted commit.
func TestManualMarkFlowFindsCulprit(t *testing.T) {
	dir, shas := bugRepo(t, 15, 11)
	startSession(t, dir, shas)
	var out string
	for i := 0; i < 10; i++ {
		raw, err := os.ReadFile(dir + "/lib.txt")
		if err != nil {
			t.Fatal(err)
		}
		verdict := "good"
		if strings.Contains(string(raw), "BUG") {
			verdict = "bad"
		}
		var code int
		var errOut string
		code, out, errOut = execCLI(t, dir, "", verdict, "--color", "never")
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s", verdict, code, errOut)
		}
		if strings.Contains(out, "first bad commit") {
			break
		}
	}
	for _, want := range []string{
		"first bad commit: " + shas[10][:7],
		"author : Dev Example <dev@example.test>",
		"subject: change 11",
		"narrowed to 1",
		"'halfstep reset' returns to main",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("culprit block missing %q:\n%s", want, out)
		}
	}
	head := gitRun(t, dir, "", "rev-parse", "HEAD")
	if head != shas[10] {
		t.Fatal("HEAD should rest on the culprit")
	}
}

func TestMarkShowsShrinkDelta(t *testing.T) {
	dir, shas := bugRepo(t, 17, 0)
	startSession(t, dir, shas)
	code, out, _ := execCLI(t, dir, "", "good", "--color", "never")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "✓ good") || !strings.Contains(out, "16 → 8 candidates") {
		t.Fatalf("delta line wrong:\n%s", out)
	}
	if !strings.Contains(out, "(step 2)") {
		t.Fatalf("should advance to step 2:\n%s", out)
	}
}

func TestSkipEverythingEndsInconclusiveExit1(t *testing.T) {
	dir, shas := bugRepo(t, 4, 0)
	startSession(t, dir, shas)
	var lastCode int
	var lastOut string
	for i := 0; i < 3; i++ {
		code, out, _ := execCLI(t, dir, "", "skip", "--color", "never")
		lastCode, lastOut = code, out
		if strings.Contains(out, "inconclusive") {
			break
		}
	}
	if lastCode != exitBisect {
		t.Fatalf("inconclusive hunt should exit 1, got %d", lastCode)
	}
	if !strings.Contains(lastOut, "inconclusive:") || !strings.Contains(lastOut, "(skipped)") {
		t.Fatalf("suspects block wrong:\n%s", lastOut)
	}
}

func TestUndoRestoresRangeAndReport(t *testing.T) {
	dir, shas := bugRepo(t, 17, 0)
	startSession(t, dir, shas)
	execCLI(t, dir, "", "good", "--color", "never")
	code, out, _ := execCLI(t, dir, "", "undo", "--color", "never")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "undid ✓ good") || !strings.Contains(out, "back to 16 candidates") {
		t.Fatalf("undo report wrong:\n%s", out)
	}
	code, _, errOut := execCLI(t, dir, "", "undo")
	if code != exitBisect || !strings.Contains(errOut, "nothing to undo") {
		t.Fatalf("empty undo: exit %d, stderr %q", code, errOut)
	}
}

// TestRunDrivesTheWholeHunt is the flagship: a test script with git
// bisect run semantics finds the planted commit unattended.
func TestRunDrivesTheWholeHunt(t *testing.T) {
	dir, shas := bugRepo(t, 30, 23)
	startSession(t, dir, shas)
	code, out, errOut := execCLI(t, dir, "", "run", "--color", "never",
		"--", "sh", "-c", "! grep -q BUG lib.txt")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "first bad commit: "+shas[22][:7]) {
		t.Fatalf("culprit wrong:\n%s", out)
	}
	if !strings.Contains(out, "step 1 ") || !strings.Contains(out, "exit ") {
		t.Fatalf("per-step progress lines missing:\n%s", out)
	}
	// 29 candidates must resolve within ceil(log2(29)) = 5 verdicts.
	if strings.Contains(out, "step 6 ") {
		t.Fatalf("took more than 5 steps for 29 candidates:\n%s", out)
	}
}

func TestRunSkips125AndStillConverges(t *testing.T) {
	dir, shas := bugRepo(t, 12, 8)
	startSession(t, dir, shas)
	// The script cannot test one specific commit and answers 125 for it.
	script := fmt.Sprintf("if [ \"$(git rev-parse HEAD)\" = %s ]; then exit 125; fi; ! grep -q BUG lib.txt", shas[5])
	code, out, errOut := execCLI(t, dir, "", "run", "--color", "never", "--", "sh", "-c", script)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "○ skip") {
		t.Fatalf("skip verdict missing:\n%s", out)
	}
	if !strings.Contains(out, "first bad commit: "+shas[7][:7]) {
		t.Fatalf("culprit wrong despite skip:\n%s", out)
	}
}

func TestRunAbortsOnExit128Plus(t *testing.T) {
	dir, shas := bugRepo(t, 8, 0)
	startSession(t, dir, shas)
	code, _, errOut := execCLI(t, dir, "", "run", "--", "sh", "-c", "exit 130")
	if code != exitRuntime || !strings.Contains(errOut, "aborting") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	// The abort must not have recorded any verdict.
	_, out, _ := execCLI(t, dir, "", "status", "--format", "json")
	if !strings.Contains(out, "\"steps\": []") {
		t.Fatalf("aborted run should leave zero steps:\n%s", out)
	}
}

func TestRunCommandNotFoundExit3(t *testing.T) {
	dir, shas := bugRepo(t, 6, 0)
	startSession(t, dir, shas)
	code, _, errOut := execCLI(t, dir, "", "run", "--", "no-such-binary-anywhere")
	if code != exitRuntime || !strings.Contains(errOut, "cannot run") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
}

func TestStatusTextShowsUnderTest(t *testing.T) {
	dir, shas := bugRepo(t, 20, 0)
	startSession(t, dir, shas)
	execCLI(t, dir, "", "good", "--color", "never")
	code, out, _ := execCLI(t, dir, "", "status", "--color", "never")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"hunting between " + shas[0][:7],
		"9 of 19 candidates",
		"under test:",
		"1 verdict so far",
		"mark it: halfstep good | halfstep bad | halfstep skip",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
}

func TestStatusJSONEnvelope(t *testing.T) {
	dir, shas := bugRepo(t, 20, 0)
	startSession(t, dir, shas)
	execCLI(t, dir, "", "good", "--color", "never")
	code, out, _ := execCLI(t, dir, "", "status", "--format", "json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Tool          string   `json:"tool"`
		SchemaVersion int      `json:"schema_version"`
		Version       string   `json:"version"`
		OriginalRef   string   `json:"original_ref"`
		InitialCount  int      `json:"initial_count"`
		Candidates    int      `json:"candidates"`
		StepsLeft     int      `json:"steps_left_estimate"`
		UnderTest     string   `json:"under_test"`
		Done          bool     `json:"done"`
		Goods         []string `json:"goods"`
		Steps         []struct {
			Verdict string `json:"verdict"`
			Before  int    `json:"before"`
			After   int    `json:"after"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	if doc.Tool != "halfstep" || doc.SchemaVersion != 1 || doc.Version != "0.1.0" {
		t.Fatalf("envelope wrong: %+v", doc)
	}
	if doc.OriginalRef != "main" || doc.InitialCount != 19 || doc.Candidates != 9 {
		t.Fatalf("counts wrong: %+v", doc)
	}
	if doc.Done || doc.UnderTest == "" || doc.StepsLeft != 4 {
		t.Fatalf("progress wrong: %+v", doc)
	}
	if len(doc.Steps) != 1 || doc.Steps[0].Verdict != "good" || doc.Steps[0].Before != 19 || doc.Steps[0].After != 9 {
		t.Fatalf("steps wrong: %+v", doc.Steps)
	}
}

func TestLogShowsShrinkChart(t *testing.T) {
	dir, shas := bugRepo(t, 15, 11)
	startSession(t, dir, shas)
	execCLI(t, dir, "", "run", "--color", "never", "--", "sh", "-c", "! grep -q BUG lib.txt")
	code, out, _ := execCLI(t, dir, "", "log", "--color", "never")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"halfstep log —",
		"step  verdict  commit   candidates",
		"0  start",
		"14 → 7",
		"first bad commit: " + shas[10][:7],
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
	// The chart must be a staircase: each row's bar no longer than the last.
	prev := 1 << 30
	for _, line := range strings.Split(out, "\n") {
		n := strings.Count(line, "█")
		if n == 0 {
			continue
		}
		if n > prev {
			t.Fatalf("shrink chart grew between rows:\n%s", out)
		}
		prev = n
	}
}

func TestResetRestoresBranchAndIsIdempotent(t *testing.T) {
	dir, shas := bugRepo(t, 8, 0)
	startSession(t, dir, shas)
	code, out, _ := execCLI(t, dir, "", "reset")
	if code != 0 || !strings.Contains(out, "back on main") {
		t.Fatalf("exit %d, out %q", code, out)
	}
	if gitRun(t, dir, "", "symbolic-ref", "--short", "HEAD") != "main" {
		t.Fatal("HEAD should be back on main")
	}
	code, out, _ = execCLI(t, dir, "", "reset")
	if code != 0 || !strings.Contains(out, "nothing to reset") {
		t.Fatalf("second reset: exit %d, out %q", code, out)
	}
}

func TestDashCTargetsAnotherDirectory(t *testing.T) {
	dir, shas := bugRepo(t, 10, 0)
	elsewhere := t.TempDir()
	code, out, errOut := execCLI(t, elsewhere, "", "-C", dir, "start",
		"--color", "never", "--bad", "HEAD", "--good", shas[0])
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	code, out, _ = execCLI(t, elsewhere, "", "-C", dir, "status", "--color", "never")
	if code != 0 || !strings.Contains(out, "9 candidates") {
		t.Fatalf("status via -C: exit %d\n%s", code, out)
	}
}

func TestColorAlwaysEmitsAnsi(t *testing.T) {
	dir, shas := bugRepo(t, 6, 0)
	code, out, _ := execCLI(t, dir, "", "start", "--color", "always",
		"--bad", "HEAD", "--good", shas[0])
	if code != 0 || !strings.Contains(out, "\x1b[32m") {
		t.Fatalf("exit %d, no green escape in:\n%q", code, out)
	}
}
