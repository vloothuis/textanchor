package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/vloothuis/textanchor/bundle"
	"github.com/vloothuis/textanchor/quotefind"
)

func authorFlag(fs *flag.FlagSet) *string {
	return fs.String("author", "", "author `name` (default $TEXTANCHOR_AUTHOR, git user.name, login name)")
}

func colorFlag(fs *flag.FlagSet) *string {
	return fs.String("color", "auto", "use colour: auto, always or never")
}

// resolveAuthor picks the author of a new comment.
func resolveAuthor(e *env, flagValue string) (string, error) {
	name := strings.TrimSpace(flagValue)
	if name == "" {
		name = strings.TrimSpace(e.getenv("TEXTANCHOR_AUTHOR"))
	}
	if name == "" {
		if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
			name = strings.TrimSpace(string(out))
		}
	}
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	if name == "" {
		return "", errors.New("no author name; use --author or set TEXTANCHOR_AUTHOR")
	}
	return name, nil
}

// readBody returns the -m value if it was given, else reads standard input.
func readBody(e *env, fs *flag.FlagSet, value string) (string, error) {
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "m" {
			given = true
		}
	})
	if !given {
		if e.stdinIsTerm() {
			fmt.Fprintln(e.stderr, "Type the comment, then press Ctrl-D on a new line.")
		}
		data, err := io.ReadAll(io.LimitReader(e.stdin, bundle.MaxBody+1))
		if err != nil {
			return "", err
		}
		if len(data) > bundle.MaxBody {
			return "", fmt.Errorf("comment is larger than %d bytes", bundle.MaxBody)
		}
		value = string(data)
	}
	value = strings.TrimSpace(bundle.NormalizeDocument(value))
	if value == "" {
		return "", errors.New("the comment is empty")
	}
	if !utf8.ValidString(value) {
		return "", errors.New("the comment is not valid UTF-8")
	}
	return value, nil
}

// findQuote locates quote in the document source. It tries the exact source
// text first. If that is absent, it matches against the rendered text, so a
// quote copied from a rendered view still works. When the exact text occurs
// more than once, nth (from 1) must choose one.
func findQuote(doc, quote string, nth int) (int, int, error) {
	quote = bundle.NormalizeDocument(quote)
	var hits []int
	for i := 0; ; {
		j := strings.Index(doc[i:], quote)
		if j < 0 {
			break
		}
		hits = append(hits, i+j)
		i += j + 1
	}
	if nth < 0 {
		return 0, 0, usageErr("--nth must be 1 or more")
	}
	switch {
	case len(hits) == 0 && nth > 1:
		return 0, 0, fmt.Errorf("the quote does not occur in the source text; --nth needs an exact match")
	case len(hits) == 0:
		start, end, ok := quotefind.Find(doc, quote)
		if !ok {
			return 0, 0, errors.New("the quote does not occur in the document")
		}
		return start, end, nil
	case nth > len(hits):
		return 0, 0, fmt.Errorf("the quote occurs %d times; --nth %d is too high", len(hits), nth)
	case nth > 0:
		return hits[nth-1], hits[nth-1] + len(quote), nil
	case len(hits) == 1:
		return hits[0], hits[0] + len(quote), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "the quote occurs %d times; choose one with --nth:", len(hits))
	for i, h := range hits {
		if i == 20 {
			fmt.Fprintf(&b, "\n  ... and %d more", len(hits)-i)
			break
		}
		fmt.Fprintf(&b, "\n  %d. line %d: %s", i+1, lineOf(doc, h), context(doc, h, h+len(quote)))
	}
	return 0, 0, errors.New(b.String())
}

// context shows the text around doc[start:end] on one line, with the match in
// brackets.
func context(doc string, start, end int) string {
	from := max(0, start-30)
	to := min(len(doc), end+30)
	for from > 0 && !utf8.RuneStart(doc[from]) {
		from--
	}
	for to < len(doc) && !utf8.RuneStart(doc[to]) {
		to++
	}
	return singleLine(doc[from:start] + "[" + doc[start:end] + "]" + doc[end:to])
}

// lineRange returns the byte range of line N, or of lines N to M, without the
// final newline. Lines count from 1.
func lineRange(doc, spec string) (int, int, error) {
	first, last, found := strings.Cut(spec, "-")
	n, err := strconv.Atoi(first)
	if err != nil || n < 1 {
		return 0, 0, usageErr("--line: %q is not a line number", spec)
	}
	m := n
	if found {
		m, err = strconv.Atoi(last)
		if err != nil || m < n {
			return 0, 0, usageErr("--line: %q is not a range N-M with N <= M", spec)
		}
	}
	starts := []int{0}
	for i := 0; i < len(doc); i++ {
		if doc[i] == '\n' && i+1 < len(doc) {
			starts = append(starts, i+1)
		}
	}
	if m > len(starts) {
		return 0, 0, fmt.Errorf("--line: the document has %d lines", len(starts))
	}
	start := starts[n-1]
	end := len(doc)
	if m < len(starts) {
		end = starts[m] - 1
	}
	if strings.TrimSpace(doc[start:end]) == "" {
		return 0, 0, errors.New("--line: the selected lines are empty")
	}
	return start, end, nil
}

// lineOf returns the 1-based line number of a byte offset.
func lineOf(doc string, off int) int {
	return 1 + strings.Count(doc[:off], "\n")
}

// preview shortens s to at most n runes on one line.
func preview(s string, n int) string {
	s = singleLine(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
