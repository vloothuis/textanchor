package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vloothuis/textanchor/bundle"
)

const doc = `# Plan

The library supports every platform that Go targets. We ship a CLI
with **colour output**.

## Risks

- The library supports every platform, but Windows is untested.
`

type harness struct {
	t   *testing.T
	dir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return &harness{t, dir}
}

func (h *harness) path(name string) string { return filepath.Join(h.dir, name) }

// run runs the CLI and returns stdout, stderr and the exit code. Paths
// starting with "@" are made relative to the test directory.
func (h *harness) runIn(stdin string, args ...string) (string, string, int) {
	h.t.Helper()
	for i, a := range args {
		if strings.HasPrefix(a, "@") {
			args[i] = h.path(a[1:])
		}
	}
	var out, errb bytes.Buffer
	e := &env{
		stdin:       strings.NewReader(stdin),
		stdout:      &out,
		stderr:      &errb,
		isTerminal:  func() bool { return false },
		stdinIsTerm: func() bool { return false },
		getenv: func(k string) string {
			if k == "TEXTANCHOR_AUTHOR" {
				return "Ada"
			}
			return ""
		},
	}
	code := run(e, args)
	return out.String(), errb.String(), code
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()
	out, errs, code := h.runIn("", args...)
	if code != 0 {
		h.t.Fatalf("%v: exit %d: %s", args, code, errs)
	}
	return out
}

func (h *harness) fail(want string, args ...string) {
	h.t.Helper()
	_, errs, code := h.runIn("", args...)
	if code == 0 || !strings.Contains(errs, want) {
		h.t.Fatalf("%v: exit %d, stderr %q; want error containing %q", args, code, errs, want)
	}
}

func (h *harness) open() *bundle.Bundle {
	h.t.Helper()
	b, err := bundle.Open(h.path("plan.mdreview"))
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

func TestWorkflow(t *testing.T) {
	h := newHarness(t)
	h.ok("init", "@plan.md")
	h.fail("exists", "init", "@plan.md")
	h.ok("init", "@plan.md", "--force")

	h.fail("occurs 2 times", "add", "@plan.mdreview", "--quote", "supports every platform", "-m", "x")
	h.ok("add", "@plan.mdreview", "--quote", "supports every platform", "--nth", "2", "-m", "Windows?")
	// Flags before the positional argument work too.
	h.ok("add", "--line", "1", "@plan.mdreview", "-m", "Title")
	// A quote copied from rendered text, without the ** markup.
	h.ok("add", "@plan.mdreview", "--quote", "with colour output", "-m", "Rendered")
	if _, errs, code := h.runIn("From stdin\n", "add", "@plan.mdreview", "--quote", "Go targets"); code != 0 {
		t.Fatal(errs)
	}

	b := h.open()
	threads := b.Threads()
	if len(threads) != 4 {
		t.Fatalf("got %d threads", len(threads))
	}
	if q := threads[0].Anchor.Quote; q != "supports every platform" || !strings.Contains(threads[0].Anchor.Suffix, "Windows") {
		t.Errorf("--nth 2 anchored the wrong occurrence: %+v", threads[0].Anchor)
	}
	if threads[1].Anchor.Quote != "# Plan" {
		t.Errorf("--line 1 quote = %q", threads[1].Anchor.Quote)
	}
	if !strings.Contains(threads[2].Anchor.Quote, "colour output") {
		t.Errorf("rendered quote = %q", threads[2].Anchor.Quote)
	}
	if threads[3].Body != "From stdin" || threads[3].Author != "Ada" {
		t.Errorf("stdin comment = %+v", threads[3])
	}

	id := threads[0].ID
	h.ok("reply", "@plan.mdreview", id[:6], "-m", "Tested on 11.", "--author", "Grace")
	h.ok("edit", "@plan.mdreview", id[:6], "-m", "Windows still?")
	h.ok("resolve", "@plan.mdreview", id[:6])
	h.fail("too short", "resolve", "@plan.mdreview", id[:2])

	out := h.ok("show", "@plan.mdreview")
	if strings.Contains(out, "Windows still?") {
		t.Error("show printed a resolved thread")
	}
	out = h.ok("show", "@plan.mdreview", "--resolved")
	for _, want := range []string{"Windows still?", "Tested on 11.", "Grace", "resolved", "[1]", "Rendered"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Error("colour codes printed to a non-terminal")
	}

	out = h.ok("list", "@plan.mdreview", "--json", "--resolved")
	var js []jsonThread
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	if len(js) != 4 || js[0].Line != 1 || len(js[len(js)-1].Replies) != 1 {
		t.Errorf("list --json = %+v", js)
	}

	h.ok("reopen", "@plan.mdreview", id)
	out = h.ok("delete", "@plan.mdreview", id)
	if !strings.Contains(out, "1 reply") {
		t.Errorf("delete output = %q", out)
	}
	if n := len(h.open().Threads()); n != 3 {
		t.Errorf("%d threads after delete", n)
	}

	if got := h.ok("extract", "@plan.mdreview"); got != doc {
		t.Errorf("extract = %q", got)
	}
	h.ok("extract", "@plan.mdreview", "-o", "@out.md")
	h.fail("exists", "extract", "@plan.mdreview", "-o", "@out.md")

	edited := strings.Replace(doc, "We ship a CLI", "We also ship a CLI", 1)
	os.WriteFile(h.path("plan2.md"), []byte(edited), 0o644)
	out = h.ok("update", "@plan.mdreview", "@plan2.md")
	if !strings.Contains(out, "comments matched the new text") {
		t.Errorf("update output = %q", out)
	}
	if h.open().Document != edited {
		t.Error("update did not replace the document")
	}
}

func TestMergeCommand(t *testing.T) {
	h := newHarness(t)
	h.ok("init", "@plan.md")
	h.ok("add", "@plan.mdreview", "--line", "1", "-m", "base")
	data, _ := os.ReadFile(h.path("plan.mdreview"))
	os.WriteFile(h.path("grace.mdreview"), data, 0o644)
	h.ok("add", "@grace.mdreview", "--line", "3", "-m", "from Grace", "--author", "Grace")
	out := h.ok("merge", "@plan.mdreview", "@grace.mdreview")
	if !strings.Contains(out, "1 added") {
		t.Errorf("merge output = %q", out)
	}
	if n := len(h.open().Threads()); n != 2 {
		t.Errorf("%d threads after merge", n)
	}
}

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	h.ok("init", "@plan.md")
	for _, args := range [][]string{
		{},
		{"nope"},
		{"show"},
		{"add", "@plan.mdreview", "-m", "x"},
		{"add", "@plan.mdreview", "--quote", "a", "--line", "1", "-m", "x"},
		{"show", "@plan.mdreview", "--color", "sometimes"},
	} {
		if _, _, code := h.runIn("", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	h.fail("empty", "add", "@plan.mdreview", "--line", "1", "-m", "  ")
	h.fail("empty", "add", "@plan.mdreview", "--line", "2", "-m", "x")
	h.fail("not found", "reply", "@plan.mdreview", "ffffffff", "-m", "x")
}

// TestHostileBundle checks that text from a bundle cannot reach the terminal
// as control sequences.
func TestHostileBundle(t *testing.T) {
	h := newHarness(t)
	evil := "\x1b]52;c;cm0gLXJmIH4=\x07\x1b[2J\u202egnp.exe\u009b31m"
	b := bundle.New("# T\n\nhello " + evil + " world\n")
	i := strings.Index(b.Document, "hello")
	c, err := b.AddComment(i, i+5, "Mallory"+evil, "body "+evil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddReply(c, evil, evil); err != nil {
		t.Fatal(err)
	}
	if err := b.Save(h.path("evil.mdreview")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"show", "@evil.mdreview", "--color", "always"},
		{"list", "@evil.mdreview", "--color", "always"},
		{"list", "@evil.mdreview", "--json"},
		{"add", "@evil.mdreview", "--quote", "zzz", "-m", "x"},
	} {
		out, errs, _ := h.runIn("", args...)
		for _, s := range []string{out, errs} {
			s = stripOwnSGR(s)
			for _, bad := range []string{"\x1b", "\x07", "\u202e", "\u009b"} {
				if strings.Contains(s, bad) {
					t.Errorf("%v: output contains %q:\n%q", args, bad, s)
				}
			}
		}
	}
	out, _, _ := h.runIn("", "show", "@evil.mdreview", "--color", "never")
	if !strings.Contains(out, `\x1b]52`) || !strings.Contains(out, `\u{202e}`) {
		t.Errorf("escapes not shown visibly:\n%s", out)
	}
}

// stripOwnSGR removes the colour codes the tool itself emits.
func stripOwnSGR(s string) string {
	for _, code := range []string{sgrDim, sgrHeading, sgrCode, sgrHighlight, sgrMarker, sgrAuthor, sgrResolved, sgrOrphan, "0"} {
		s = strings.ReplaceAll(s, "\x1b["+code+"m", "")
	}
	return s
}

func TestExtractToTerminalIsSanitized(t *testing.T) {
	h := newHarness(t)
	if err := bundle.New("a\x1b]52;c;eA==\x07b\n").Save(h.path("evil.mdreview")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	e := &env{stdout: &out, stderr: &out, isTerminal: func() bool { return true }, getenv: func(string) string { return "" }}
	if code := run(e, []string{"extract", h.path("evil.mdreview")}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") || !strings.Contains(out.String(), `\x1b]52`) {
		t.Errorf("terminal output = %q", out.String())
	}
	// A pipe gets the exact bytes.
	if got, _, _ := h.runIn("", "extract", "@evil.mdreview"); got != "a\x1b]52;c;eA==\x07b\n" {
		t.Errorf("piped output = %q", got)
	}
}

func TestSanitize(t *testing.T) {
	tests := map[string]string{
		"plain text\twith tab\n":       "plain text\twith tab\n",
		"\x1b[31mred":                  `\x1b[31mred`,
		"bell\x07":                     `bell\x07`,
		"del\x7f":                      `del\x7f`,
		"c1\u009b":                     `c1\u{009b}`,
		"rtl\u202eabc":                 `rtl\u{202e}abc`,
		"iso\u2066x\u2069":             `iso\u{2066}x\u{2069}`,
		"zw\u200bsp":                   `zw\u{200b}sp`,
		"bad\xffutf8":                  "bad\ufffdutf8",
		"emoji 👩\u200d💻 and accents é": "emoji 👩\u200d💻 and accents é",
		"cr\r":                         `cr\x0d`,
	}
	for in, want := range tests {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLineRange(t *testing.T) {
	d := "one\ntwo\n\nfour"
	for spec, want := range map[string]string{"1": "one", "2": "two", "1-2": "one\ntwo", "4": "four", "2-4": "two\n\nfour"} {
		s, e, err := lineRange(d, spec)
		if err != nil || d[s:e] != want {
			t.Errorf("lineRange(%q) = %q, %v; want %q", spec, d[s:e], err, want)
		}
	}
	for _, spec := range []string{"0", "5", "x", "3-2", "3"} {
		if _, _, err := lineRange(d, spec); err == nil {
			t.Errorf("lineRange(%q) succeeded", spec)
		}
	}
}
