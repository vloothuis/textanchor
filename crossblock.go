package textanchor

import (
	"sort"
	"strings"
)

// Tuning constants for the cross-block phase (phase 3).
const (
	// crossBlockMaxChunks is the most paragraphs a quote may span and still
	// enter phase 3. It bounds the pair enumeration below, which is linear in
	// the document times this number.
	crossBlockMaxChunks = 32

	// crossBlockSlack is how many paragraphs may have been inserted inside
	// the quoted range since the anchor was made.
	crossBlockSlack = 2

	// crossBlockGapPenalty is subtracted from the score for each paragraph
	// inserted into or deleted from the middle of the range.
	crossBlockGapPenalty = 0.15

	// crossBlockProbeRunes caps the endpoint text used to SCREEN paragraphs.
	// Screening runs once per paragraph of the document, so it must be cheap
	// and independent of the quote length.
	crossBlockProbeRunes = 48

	// crossBlockScreenFloor is the probe similarity a paragraph needs to be
	// considered as an endpoint at all. Lower than the final floor, because a
	// probe sees only part of the endpoint.
	crossBlockScreenFloor = 0.5

	// crossBlockShortlist is how many paragraph pairs are fully evaluated.
	crossBlockShortlist = 8

	// crossBlockEndpointRunes caps the endpoint text aligned in full
	// evaluation, so a long endpoint costs a bounded alignment. A longer
	// endpoint is aligned by the part nearest the block boundary; its far edge
	// is then placed by length, which may be off by the length of an edit.
	crossBlockEndpointRunes = 512

	// crossBlockExactBelow is the endpoint length, in runes, under which an
	// endpoint must match exactly. A short endpoint is usually a heading, and
	// the similarity floor admits two edits in five runes, enough for "Risks"
	// to match the tail of "tasks" in an unrelated paragraph.
	crossBlockExactBelow = 12
)

// findCrossBlock finds fuzzy candidates for a quote that spans several
// paragraphs (phase 3).
//
// Such a quote is a run of paragraphs q[0..k): it starts inside q[0] and runs
// to that paragraph's end, covers q[1..k-1) whole, and ends inside q[k-1],
// which it starts at the beginning of. That is the shape of a W3C
// RangeSelector, and it is matched the same way: each endpoint is located on
// its own, then the pair is checked for order and for what lies between.
//
//   - The start endpoint q[0] is matched against the END of a document
//     paragraph i, and the end endpoint q[k-1] against the START of a later
//     paragraph j, with i < j <= i+k-1+crossBlockSlack.
//   - The middle q[1..k-1) is compared with paragraphs i+1..j-1 as a whole.
//   - Each paragraph inserted or deleted in the middle costs
//     crossBlockGapPenalty.
//
// The result is accepted only if each endpoint, the middle, AND the
// length-weighted combination reach fuzzyMinSimilarity, and an endpoint shorter
// than crossBlockExactBelow matches exactly. Context scoring later adds up to 0.6
// for prefix, suffix and structure, so without this floor surrounding text
// alone could place an anchor on unrelated content.
//
// Paragraphs are blank-line chunks, the same unit phase 2 uses, and the quote
// is split the same way. Nothing is parsed: a quote is a fragment of source,
// and a markdown parser reads a fragment differently from the document it came
// from (an ordered list starting at 2, a table without its delimiter row).
// Block markup such as "## " or "- " sits inside the chunks on both sides, so
// it costs nothing in the comparison.
//
// A quote that is a single chunk, including any quote without a blank line,
// returns nil; phases 1 and 2 cover it.
func findCrossBlock(d *Document, quote string) []candidate {
	var al aligner
	return findCrossBlockWith(d, quote, &al)
}

// findCrossBlockWith is [findCrossBlock] with the caller's aligner, so a test
// can read how much alignment work a resolve did.
func findCrossBlockWith(d *Document, quote string, al *aligner) []candidate {
	q := quoteChunks(quote)
	k := len(q)
	if k < 2 || k > crossBlockMaxChunks || len(d.chunks) < 2 {
		return nil
	}
	first, last := q[0], q[k-1]
	mids := make([]string, 0, k-2)
	for _, m := range q[1 : k-1] {
		mids = append(mids, string(m))
	}
	middle := strings.Join(mids, " ")

	pairs := screenPairs(al, d.chunks, first, last, k)

	var out []candidate
	for _, p := range pairs {
		if c, ok := evaluatePair(al, d.chunks, p.i, p.j, first, last, middle, k); ok {
			out = append(out, c)
		}
	}
	return out
}

// quoteChunks splits a quote into its collapsed blank-line chunks.
func quoteChunks(quote string) [][]rune {
	var out [][]rune
	for _, p := range splitParagraphsWithOffsets(quote) {
		if t := collapseWhitespace(p.text).text; t != "" {
			out = append(out, []rune(t))
		}
	}
	return out
}

// pair is a start paragraph i and an end paragraph j, with the screening score
// that ranked it.
type pair struct {
	i, j  int
	score float64
}

// screenPairs scores every paragraph as a start and as an end endpoint with a
// short probe, then returns the best crossBlockShortlist pairs that fit the
// quote's shape.
func screenPairs(al *aligner, chunks []chunk, first, last []rune, k int) []pair {
	startProbe := tailRunes(first, crossBlockProbeRunes)
	endProbe := headRunes(last, crossBlockProbeRunes)

	asStart := make([]float64, len(chunks))
	asEnd := make([]float64, len(chunks))
	for n, ch := range chunks {
		_, asStart[n] = al.atEnd(startProbe, ch.runes)
		_, asEnd[n] = al.atStart(endProbe, ch.runes)
	}

	// Weight each endpoint by its length, so a short endpoint (a three-letter
	// heading) does not count as much evidence as a full paragraph.
	w0, wk := float64(len(startProbe)), float64(len(endProbe))

	var pairs []pair
	maxSpan := k - 1 + crossBlockSlack
	for i := range chunks {
		if asStart[i] < crossBlockScreenFloor {
			continue
		}
		for j := i + 1; j < len(chunks) && j <= i+maxSpan; j++ {
			if asEnd[j] < crossBlockScreenFloor {
				continue
			}
			pairs = append(pairs, pair{i: i, j: j, score: (w0*asStart[i] + wk*asEnd[j]) / (w0 + wk)})
		}
	}

	sort.SliceStable(pairs, func(a, b int) bool { return pairs[a].score > pairs[b].score })
	if len(pairs) > crossBlockShortlist {
		pairs = pairs[:crossBlockShortlist]
	}
	return pairs
}

// evaluatePair aligns the full endpoints in paragraphs i and j, compares the
// middle, and returns the candidate if it clears every floor.
func evaluatePair(
	al *aligner, chunks []chunk, i, j int, first, last []rune, middle string, k int,
) (candidate, bool) {
	start, e0 := locateStart(al, chunks[i], first)
	end, ek := locateEnd(al, chunks[j], last)
	if !e0.holds(len(first)) || !ek.holds(len(last)) {
		return candidate{}, false
	}

	// Each endpoint counts by the runes actually compared, not its full
	// length: an unchecked stretch is no evidence either way.
	sum, weight := e0.weight*e0.sim+ek.weight*ek.sim, e0.weight+ek.weight

	// The middle counts by the QUOTE's middle length. A paragraph inserted
	// into a range that had no middle is charged by the gap penalty below, not
	// here, where it would weigh in at its own length and sink a range whose
	// endpoints are intact.
	//
	// A quote WITH a middle needs that middle to be recognisably present on
	// its own. Folded into the total, a short middle ("Do not reboot.") is
	// outweighed by long intact endpoints, and the anchor would land on
	// whatever replaced it.
	if middle != "" {
		if j == i+1 {
			return candidate{}, false
		}
		var docMiddle []string
		for n := i + 1; n < j; n++ {
			docMiddle = append(docMiddle, chunks[n].c.text)
		}
		sm := similarity(middle, strings.Join(docMiddle, " "))
		if sm < fuzzyMinSimilarity {
			return candidate{}, false
		}
		wm := float64(len([]rune(middle)))
		sum += wm * sm
		weight += wm
	}

	gaps := abs((j - i - 1) - (k - 2))
	score := sum/weight - crossBlockGapPenalty*float64(gaps)
	if score < fuzzyMinSimilarity {
		return candidate{}, false
	}

	rng := Range{Start: chunks[i].offset + start, End: chunks[j].offset + end}
	if rng.Start >= rng.End {
		return candidate{}, false
	}
	return candidate{rng: rng, score: score * fuzzyPenalty}, true
}

// endpointMatch is how well an endpoint aligned: its similarity over the
// weight runes that were compared.
type endpointMatch struct {
	sim    float64
	weight float64
}

// holds reports whether an endpoint of n runes matched well enough to count.
func (m endpointMatch) holds(n int) bool {
	if n < crossBlockExactBelow {
		return m.sim == 1
	}
	return m.sim >= fuzzyMinSimilarity
}

// combine merges the match of an endpoint's far edge into m.
func (m endpointMatch) combine(far endpointMatch) endpointMatch {
	w := m.weight + far.weight
	return endpointMatch{sim: (m.sim*m.weight + far.sim*far.weight) / w, weight: w}
}

// locateStart finds where the start endpoint q begins in ch, aligning q
// against the end of the paragraph. Returns a byte offset into ch's RAW text
// and how well it matched.
//
// An endpoint longer than crossBlockEndpointRunes is aligned by its tail, next
// to the block boundary, and its start is placed by length. The text at that
// start is then checked against the endpoint's head, so a paragraph whose
// beginning was rewritten does not pass on its intact tail alone.
func locateStart(al *aligner, ch chunk, q []rune) (int, endpointMatch) {
	probe := tailRunes(q, crossBlockEndpointRunes)
	n, sim := al.atEnd(probe, ch.runes)
	m := endpointMatch{sim: sim, weight: float64(len(probe))}
	from := len(ch.runes) - n - (len(q) - len(probe))
	if from < 0 {
		from = 0
	}
	if rest := q[:len(q)-len(probe)]; len(rest) > 0 {
		far := headRunes(rest, crossBlockEndpointRunes)
		_, farSim := al.atStart(far, ch.runes[from:])
		m = m.combine(endpointMatch{sim: farSim, weight: float64(len(far))})
	}
	for from < len(ch.runes) && ch.runes[from] == ' ' {
		from++
	}
	b := ch.runeByte[from]
	return ch.c.sourceRange(b, len(ch.c.text)).Start, m
}

// locateEnd finds where the end endpoint q ends in ch, aligning q against the
// start of the paragraph. Returns an exclusive byte offset into ch's RAW text
// and how well it matched. A long endpoint is checked at its far edge as in
// [locateStart].
func locateEnd(al *aligner, ch chunk, q []rune) (int, endpointMatch) {
	probe := headRunes(q, crossBlockEndpointRunes)
	n, sim := al.atStart(probe, ch.runes)
	m := endpointMatch{sim: sim, weight: float64(len(probe))}
	to := n + (len(q) - len(probe))
	if to > len(ch.runes) {
		to = len(ch.runes)
	}
	if rest := q[len(probe):]; len(rest) > 0 {
		far := tailRunes(rest, crossBlockEndpointRunes)
		_, farSim := al.atEnd(far, ch.runes[:to])
		m = m.combine(endpointMatch{sim: farSim, weight: float64(len(far))})
	}
	for to > 0 && ch.runes[to-1] == ' ' {
		to--
	}
	b := ch.runeByte[to]
	return ch.c.sourceRange(0, b).End, m
}

// aligner computes endpoint alignments, reusing its buffers across calls.
// Screening aligns against every paragraph of the document, and allocating per
// paragraph was most of its cost.
type aligner struct {
	prev, cur  []int
	qRev, tRev []rune

	// cells counts edit-distance cells computed, the unit of work the cost
	// bound in the tests is stated in.
	cells int
}

// atStart returns how many leading runes of text best match q, and the
// similarity of that match: max over n of 1 - lev(q, text[:n]) / max(|q|, n).
//
// One edit-distance table answers it for every n at once, so the cost is
// |q| x |window|, where the window is text cut to the longest prefix that
// could score above zero.
func (a *aligner) atStart(q, text []rune) (int, float64) {
	if len(q) == 0 || len(text) == 0 {
		return 0, 0
	}
	return a.align(q, text[:min(alignWindow(q), len(text))])
}

// atEnd is [aligner.atStart] from the other end: how many trailing runes of
// text best match q. Both are reversed into reused buffers, which aligns their
// ends with the same table.
func (a *aligner) atEnd(q, text []rune) (int, float64) {
	if len(q) == 0 || len(text) == 0 {
		return 0, 0
	}
	a.qRev = reverseInto(a.qRev, q)
	a.tRev = reverseInto(a.tRev, tailRunes(text, alignWindow(q)))
	return a.align(a.qRev, a.tRev)
}

// alignWindow is the longest stretch of text that could align with q above
// zero similarity, plus a little room for insertions.
func alignWindow(q []rune) int { return len(q) + len(q)/2 + 4 }

func (a *aligner) align(q, t []rune) (int, float64) {
	if cap(a.prev) < len(t)+1 {
		a.prev = make([]int, len(t)+1)
		a.cur = make([]int, len(t)+1)
	}
	a.cells += len(q) * len(t)
	prev, cur := a.prev[:len(t)+1], a.cur[:len(t)+1]
	for c := range prev {
		prev[c] = c
	}
	for r := 1; r <= len(q); r++ {
		cur[0] = r
		qr := q[r-1]
		for c := 1; c <= len(t); c++ {
			cost := 1
			if qr == t[c-1] {
				cost = 0
			}
			cur[c] = min(prev[c]+1, cur[c-1]+1, prev[c-1]+cost)
		}
		prev, cur = cur, prev
	}

	// On a tie the longer match wins, so a start is not placed inside a word
	// when the runes before it score the same.
	bestN, bestSim := 0, 0.0
	for n := 1; n <= len(t); n++ {
		sim := 1 - float64(prev[n])/float64(max(len(q), n))
		if sim > 0 && sim >= bestSim {
			bestN, bestSim = n, sim
		}
	}
	return bestN, bestSim
}

// reverseInto writes r back to front into buf, growing it if needed.
func reverseInto(buf, r []rune) []rune {
	buf = append(buf[:0], r...)
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}

func headRunes(r []rune, n int) []rune {
	if len(r) <= n {
		return r
	}
	return r[:n]
}

func tailRunes(r []rune, n int) []rune {
	if len(r) <= n {
		return r
	}
	return r[len(r)-n:]
}
