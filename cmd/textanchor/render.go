package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vloothuis/textanchor"
	"github.com/vloothuis/textanchor/bundle"
)

// style emits ANSI colour codes, or nothing when colour is off.
type style struct{ on bool }

const (
	sgrDim       = "2"
	sgrHeading   = "1;34"
	sgrCode      = "36"
	sgrHighlight = "30;43"
	sgrMarker    = "1;35"
	sgrAuthor    = "1"
	sgrResolved  = "32"
	sgrOrphan    = "1;31"
)

func newStyle(e *env, when string) (style, error) {
	switch when {
	case "always":
		return style{true}, nil
	case "never":
		return style{false}, nil
	case "auto":
		return style{e.getenv("NO_COLOR") == "" && e.isTerminal()}, nil
	}
	return style{}, usageErr("--color must be auto, always or never")
}

// paint wraps already sanitized text in a colour.
func (s style) paint(sgr, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + sgr + "m" + text + "\x1b[0m"
}

// located is a thread with its position in the current document.
type located struct {
	c       *bundle.Comment
	n       int
	res     textanchor.ResolveResult
	replies []*bundle.Comment
}

// locate resolves the threads to show, numbered in document order with
// orphaned threads last.
func locate(b *bundle.Bundle, includeResolved bool) []located {
	var threads []*bundle.Comment
	for _, c := range b.Threads() {
		if includeResolved || !c.Resolved {
			threads = append(threads, c)
		}
	}
	doc := textanchor.NewDocument(b.Document)
	out := make([]located, len(threads))
	for i, c := range threads {
		out[i] = located{c: c, res: doc.Resolve(*c.Anchor, nil), replies: b.Replies(c.ID)}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].res.Range, out[j].res.Range
		switch {
		case a == nil || b == nil:
			return a != nil && b == nil
		case a.Start != b.Start:
			return a.Start < b.Start
		}
		return a.End < b.End
	})
	for i := range out {
		out[i].n = i + 1
	}
	return out
}

// show prints the document with commented spans highlighted, each thread
// printed under the line where its span ends, and orphans at the end.
func show(w io.Writer, st style, b *bundle.Bundle, threads []located) {
	doc := b.Document
	hl := highlights(threads)
	markers := map[int][]int{}
	byLine := map[int][]located{}
	var orphans []located
	for _, t := range threads {
		if t.res.Range == nil {
			orphans = append(orphans, t)
			continue
		}
		end := t.res.Range.End
		markers[end] = append(markers[end], t.n)
		line := lineOf(doc, max(t.res.Range.Start, end-1))
		byLine[line] = append(byLine[line], t)
	}

	inFence := false
	fence := ""
	lineNo := 0
	for start := 0; start < len(doc) || lineNo == 0; {
		lineNo++
		end := strings.IndexByte(doc[start:], '\n')
		next := len(doc)
		if end < 0 {
			end = len(doc)
		} else {
			end += start
			next = end + 1
		}
		line := doc[start:end]
		base := ""
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case inFence:
			base = sgrCode
			if strings.HasPrefix(trimmed, fence) {
				inFence = false
			}
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			inFence, fence, base = true, trimmed[:3], sgrCode
		case strings.HasPrefix(trimmed, "#"):
			base = sgrHeading
		}
		writeLine(w, st, doc, start, end, base, hl, markers)
		fmt.Fprintln(w)
		for _, t := range byLine[lineNo] {
			writeThread(w, st, t, "    ")
		}
		start = next
		if next == len(doc) {
			break
		}
	}

	if len(orphans) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.paint(sgrOrphan, "Comments whose text was not found in the document:"))
		for _, t := range orphans {
			writeThread(w, st, t, "    ")
		}
	}
	if len(threads) == 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, st.paint(sgrDim, "No open comments."))
	}
}

// span is a highlighted byte range.
type span struct{ start, end int }

// highlights returns the union of the located ranges, sorted.
func highlights(threads []located) []span {
	var spans []span
	for _, t := range threads {
		if r := t.res.Range; r != nil && r.End > r.Start {
			spans = append(spans, span{r.Start, r.End})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var out []span
	for _, s := range spans {
		if n := len(out); n > 0 && s.start <= out[n-1].end {
			out[n-1].end = max(out[n-1].end, s.end)
			continue
		}
		out = append(out, s)
	}
	return out
}

// writeLine prints doc[start:end] with the base style, highlights and markers.
func writeLine(w io.Writer, st style, doc string, start, end int, base string, hl []span, markers map[int][]int) {
	// Cut points: highlight edges and marker positions inside the line.
	cuts := []int{start, end}
	for _, s := range hl {
		for _, p := range []int{s.start, s.end} {
			if p > start && p < end {
				cuts = append(cuts, p)
			}
		}
	}
	for p := range markers {
		if p > start && p <= end {
			cuts = append(cuts, p)
		}
	}
	sort.Ints(cuts)
	cuts = slices.Compact(cuts)
	for i := 0; i+1 < len(cuts); i++ {
		a, b := cuts[i], cuts[i+1]
		if a < b {
			text := sanitize(doc[a:b])
			if inSpans(hl, a) {
				if st.on {
					fmt.Fprint(w, st.paint(sgrHighlight, text))
				} else {
					fmt.Fprint(w, text)
				}
			} else if base != "" {
				fmt.Fprint(w, st.paint(base, text))
			} else {
				fmt.Fprint(w, text)
			}
		}
		if ns, ok := markers[b]; ok && b > start {
			fmt.Fprint(w, st.paint(sgrMarker, markerText(ns)))
		}
	}
	if ns, ok := markers[start]; ok && start == end {
		fmt.Fprint(w, st.paint(sgrMarker, markerText(ns)))
	}
}

func inSpans(spans []span, p int) bool {
	i := sort.Search(len(spans), func(i int) bool { return spans[i].end > p })
	return i < len(spans) && spans[i].start <= p
}

func markerText(ns []int) string {
	var b strings.Builder
	for _, n := range ns {
		fmt.Fprintf(&b, "[%d]", n)
	}
	return b.String()
}

// writeThread prints a thread as an indented block with a gutter.
func writeThread(w io.Writer, st style, t located, indent string) {
	gutter := indent + st.paint(sgrMarker, "│") + " "
	c := t.c
	head := st.paint(sgrMarker, fmt.Sprintf("[%d]", t.n)) + " " + header(st, c)
	if c.Resolved {
		head += " " + st.paint(sgrResolved, "resolved")
	}
	fmt.Fprintln(w, gutter+head)
	if t.res.Range == nil {
		fmt.Fprintln(w, gutter+st.paint(sgrDim, "on: “"+preview(c.Anchor.Quote, 70)+"”"))
	} else if t.res.Confidence < bundle.ReanchorConfidence {
		fmt.Fprintln(w, gutter+st.paint(sgrDim, fmt.Sprintf("was: “%s” (%.0f%% match)", preview(c.Anchor.Quote, 60), 100*t.res.Confidence)))
	}
	writeBody(w, gutter, c.Body)
	for _, r := range t.replies {
		fmt.Fprintln(w, gutter+"  "+st.paint(sgrDim, "↳")+" "+header(st, r))
		writeBody(w, gutter+"    ", r.Body)
	}
	fmt.Fprintln(w)
}

func header(st style, c *bundle.Comment) string {
	meta := c.Created.Local().Format("2006-01-02 15:04") + " · " + c.ID
	if !c.Updated.IsZero() && c.Updated.Sub(c.Created) > time.Minute {
		meta += " · edited"
	}
	return st.paint(sgrAuthor, singleLine(c.Author)) + " " + st.paint(sgrDim, meta)
}

func writeBody(w io.Writer, prefix, body string) {
	for _, line := range strings.Split(sanitize(body), "\n") {
		fmt.Fprintln(w, strings.TrimRight(prefix+line, " "))
	}
}

// list prints the threads without the document.
func list(w io.Writer, st style, b *bundle.Bundle, threads []located) {
	if len(threads) == 0 {
		fmt.Fprintln(w, st.paint(sgrDim, "No open comments."))
		return
	}
	for _, t := range threads {
		where := "not found"
		if t.res.Range != nil {
			where = fmt.Sprintf("line %d", lineOf(b.Document, t.res.Range.Start))
		}
		fmt.Fprintln(w, st.paint(sgrMarker, fmt.Sprintf("[%d]", t.n))+" "+header(st, t.c)+" "+st.paint(sgrDim, "· "+where))
		if t.c.Resolved {
			fmt.Fprintln(w, "    "+st.paint(sgrResolved, "resolved"))
		}
		fmt.Fprintln(w, "    "+st.paint(sgrDim, "“"+preview(t.c.Anchor.Quote, 70)+"”"))
		writeBody(w, "    ", t.c.Body)
		for _, r := range t.replies {
			fmt.Fprintln(w, "      "+st.paint(sgrDim, "↳")+" "+header(st, r))
			writeBody(w, "        ", r.Body)
		}
		fmt.Fprintln(w)
	}
}

type jsonReply struct {
	ID      string    `json:"id"`
	Author  string    `json:"author"`
	Created time.Time `json:"created"`
	Body    string    `json:"body"`
}

type jsonThread struct {
	Number     int         `json:"number"`
	ID         string      `json:"id"`
	Author     string      `json:"author"`
	Created    time.Time   `json:"created"`
	Body       string      `json:"body"`
	Resolved   bool        `json:"resolved"`
	Quote      string      `json:"quote"`
	Orphaned   bool        `json:"orphaned"`
	Line       int         `json:"line,omitempty"`
	Start      int         `json:"start,omitempty"`
	End        int         `json:"end,omitempty"`
	Confidence float64     `json:"confidence"`
	Replies    []jsonReply `json:"replies"`
}

// listJSON prints the threads as JSON. Characters that could affect a
// terminal are escaped, which keeps the JSON valid.
func listJSON(w io.Writer, b *bundle.Bundle, threads []located) error {
	out := make([]jsonThread, 0, len(threads))
	for _, t := range threads {
		j := jsonThread{
			Number:     t.n,
			ID:         t.c.ID,
			Author:     t.c.Author,
			Created:    t.c.Created,
			Body:       t.c.Body,
			Resolved:   t.c.Resolved,
			Quote:      t.c.Anchor.Quote,
			Orphaned:   t.res.Orphaned,
			Confidence: t.res.Confidence,
			Replies:    []jsonReply{},
		}
		if r := t.res.Range; r != nil {
			j.Line, j.Start, j.End = lineOf(b.Document, r.Start), r.Start, r.End
		}
		for _, r := range t.replies {
			j.Replies = append(j.Replies, jsonReply{r.ID, r.Author, r.Created, r.Body})
		}
		out = append(out, j)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	var buf strings.Builder
	for _, r := range string(data) {
		if unsafeRune(r) || r == 0x7f {
			fmt.Fprintf(&buf, `\u%04x`, r)
		} else {
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('\n')
	_, err = io.WriteString(w, buf.String())
	return err
}
