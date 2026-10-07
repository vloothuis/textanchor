package textanchor

import "unicode/utf8"

// Document is a document prepared for resolving anchors against it.
//
// Resolving needs several per-document structures: the whitespace-collapsed
// form, the paragraph table and the heading positions. Building them is linear
// in the document and independent of the anchor, so a caller resolving many
// anchors against one document builds a Document once and calls
// [Document.Resolve] for each. [Resolve] and [ResolveAll] are wrappers that do
// exactly that.
//
// A Document is immutable after construction and safe for concurrent use.
type Document struct {
	text      string
	collapsed collapsed
	chunks    []chunk
	headings  []headingPos
	paraStart []int
}

// chunk is one blank-line-delimited paragraph of a document, prepared for
// both fuzzy phases.
type chunk struct {
	paragraph

	// c is the paragraph collapsed on its own, so offsets map back into it.
	c collapsed

	// runes is c.text as runes, and runeByte[i] is the byte offset of runes[i]
	// in c.text, plus a sentinel holding len(c.text). The cross-block phase
	// aligns in runes and maps back through this table.
	runes    []rune
	runeByte []int
}

// NewDocument prepares text for resolving anchors against it.
func NewDocument(text string) *Document {
	paras := splitParagraphsWithOffsets(text)
	chunks := make([]chunk, 0, len(paras))
	for _, p := range paras {
		c := collapseWhitespace(p.text)
		if c.text == "" {
			continue
		}
		runes := make([]rune, 0, utf8.RuneCountInString(c.text))
		runeByte := make([]int, 0, cap(runes)+1)
		for i, r := range c.text {
			runes = append(runes, r)
			runeByte = append(runeByte, i)
		}
		runeByte = append(runeByte, len(c.text))
		chunks = append(chunks, chunk{paragraph: p, c: c, runes: runes, runeByte: runeByte})
	}
	return &Document{
		text:      text,
		collapsed: collapseWhitespace(text),
		chunks:    chunks,
		headings:  extractHeadingPositions(text),
		paraStart: extractParagraphBoundaries(text),
	}
}

// Text returns the document the Document was prepared from.
func (d *Document) Text() string { return d.text }

// Resolve locates anchor in the document. See the package-level [Resolve] for
// the algorithm and the meaning of the result.
func (d *Document) Resolve(anchor Anchor, opts *ResolveOptions) ResolveResult {
	if opts == nil {
		opts = DefaultResolveOptions()
	}

	candidates := findCandidates(d, anchor)
	if len(candidates) == 0 {
		return ResolveResult{
			Orphaned:     true,
			OrphanReason: "quote not found in document",
		}
	}

	for i := range candidates {
		candidates[i].score = scoreCandidateWithCache(
			d.text, anchor, candidates[i], opts,
			d.headings, d.paraStart, d.collapsed.text,
		)
	}

	best := candidates[0]
	for _, c := range candidates[1:] {
		if c.score > best.score {
			best = c
		}
	}

	if best.score < opts.MinConfidence {
		return ResolveResult{
			Orphaned:     true,
			OrphanReason: "confidence too low",
			Confidence:   best.score,
		}
	}

	return ResolveResult{
		Range:      &Range{Start: best.rng.Start, End: best.rng.End},
		Confidence: best.score,
	}
}
