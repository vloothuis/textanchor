package bundle

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vloothuis/textanchor"
)

// ReanchorConfidence is the confidence at or above which [Bundle.SetDocument]
// and [Bundle.Merge] replace an anchor with one made from the new document
// when the quoted text has changed. Below it the old anchor is kept, so a weak
// match cannot overwrite the original quote. An anchor whose quote is found
// unchanged is always replaced.
const ReanchorConfidence = 0.9

// ErrNotFound is returned when no comment matches an id or id prefix.
var ErrNotFound = errors.New("comment not found")

// NewID returns a random 64-bit id, in hexadecimal, that no comment in the
// bundle uses.
func (b *Bundle) NewID() string {
	for {
		var buf [8]byte
		_, _ = rand.Read(buf[:]) // crypto/rand.Read never fails
		id := hex.EncodeToString(buf[:])
		if b.Get(id) == nil {
			return id
		}
	}
}

// Get returns the comment with exactly this id, or nil.
func (b *Bundle) Get(id string) *Comment {
	for _, c := range b.Comments {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Find returns the live comment whose id is id or starts with id. It fails
// when id is shorter than minPrefix and not a full id, or when no comment or
// more than one matches.
func (b *Bundle) Find(id string, minPrefix int) (*Comment, error) {
	if c := b.Get(id); c != nil && !c.Deleted {
		return c, nil
	}
	if len(id) < minPrefix {
		return nil, fmt.Errorf("id %q is too short; give at least %d characters", id, minPrefix)
	}
	var match *Comment
	for _, c := range b.Comments {
		if c.Deleted || !strings.HasPrefix(c.ID, id) {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("id %q matches more than one comment", id)
		}
		match = c
	}
	if match == nil {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return match, nil
}

// Threads returns the live comments that start threads, in bundle order.
func (b *Bundle) Threads() []*Comment {
	var out []*Comment
	for _, c := range b.Comments {
		if c.Parent == "" && !c.Deleted {
			out = append(out, c)
		}
	}
	return out
}

// Replies returns the live replies to the comment with the given id, in bundle
// order.
func (b *Bundle) Replies(id string) []*Comment {
	var out []*Comment
	for _, c := range b.Comments {
		if c.Parent == id && !c.Deleted {
			out = append(out, c)
		}
	}
	return out
}

// AddComment starts a thread on document[start:end].
func (b *Bundle) AddComment(start, end int, author, body string) (*Comment, error) {
	a, err := textanchor.New(b.Document, start, end, nil)
	if err != nil {
		return nil, err
	}
	c := &Comment{
		ID:      b.NewID(),
		Author:  author,
		Created: now(),
		Body:    body,
		Anchor:  &a,
	}
	return c, b.add(c)
}

// AddReply adds a reply to the thread that parent belongs to.
func (b *Bundle) AddReply(parent *Comment, author, body string) (*Comment, error) {
	root := parent.ID
	if parent.Parent != "" {
		root = parent.Parent
	}
	c := &Comment{
		ID:      b.NewID(),
		Parent:  root,
		Author:  author,
		Created: now(),
		Body:    body,
	}
	return c, b.add(c)
}

func (b *Bundle) add(c *Comment) error {
	b.Comments = append(b.Comments, c)
	if err := b.Validate(); err != nil {
		b.Comments = b.Comments[:len(b.Comments)-1]
		return err
	}
	return nil
}

// Delete turns the comment into a tombstone. Deleting a comment that starts a
// thread also deletes its replies.
func (b *Bundle) Delete(c *Comment) {
	t := now()
	for _, d := range b.Comments {
		if d == c || (c.Parent == "" && d.Parent == c.ID) {
			d.Deleted = true
			d.Body = ""
			d.Resolved = false
			d.Anchor = nil
			d.Updated = t
		}
	}
}

// Relocation reports what happened to one comment's anchor when the document
// changed.
type Relocation struct {
	Comment *Comment
	Result  textanchor.ResolveResult
	// Reanchored is true when the anchor was replaced by one made from the
	// new document.
	Reanchored bool
}

// SetDocument replaces the document and re-anchors every live thread whose
// quote it finds unchanged, or finds changed with confidence of at least
// [ReanchorConfidence]. Other threads keep their anchor. It returns one
// Relocation per live thread.
func (b *Bundle) SetDocument(document string) []Relocation {
	document = NormalizeDocument(document)
	threads := b.Threads()
	out := relocate(document, threads)
	b.Document = document
	return out
}

func relocate(document string, threads []*Comment) []Relocation {
	anchors := make([]textanchor.Anchor, len(threads))
	for i, c := range threads {
		anchors[i] = *c.Anchor
	}
	results := textanchor.ResolveAll(document, anchors, nil)
	out := make([]Relocation, len(threads))
	for i, res := range results {
		c := threads[i]
		out[i] = Relocation{Comment: c, Result: res}
		if res.Orphaned {
			continue
		}
		// An unchanged quote loses nothing when re-anchored; only its context
		// is refreshed. A changed quote needs a confident match.
		same := document[res.Range.Start:res.Range.End] == c.Anchor.Quote
		if !same && res.Confidence < ReanchorConfidence {
			continue
		}
		a, err := textanchor.New(document, res.Range.Start, res.Range.End, nil)
		if err != nil {
			continue
		}
		c.Anchor = &a
		out[i].Reanchored = true
	}
	return out
}

// MergeResult counts what [Bundle.Merge] changed.
type MergeResult struct {
	Added   int
	Updated int
	Deleted int
	// Relocations is set when the other bundle has a different document: it
	// reports how each added thread was anchored to this bundle's document.
	Relocations []Relocation
}

// Merge adds the comments of other to b. Comments are matched by id. When
// both hold the same id, a tombstone wins; otherwise the copy changed last
// wins. When other has a different document, added threads are re-anchored
// to b's document as [Bundle.SetDocument] does. other is not changed.
func (b *Bundle) Merge(other *Bundle) (MergeResult, error) {
	var res MergeResult
	before := b.Comments
	merged := make([]*Comment, len(before))
	copy(merged, before)
	index := make(map[string]int, len(merged))
	for i, c := range merged {
		index[c.ID] = i
	}
	var added []*Comment
	for _, theirs := range other.Comments {
		i, ok := index[theirs.ID]
		if !ok {
			c := theirs.clone()
			index[c.ID] = len(merged)
			merged = append(merged, c)
			res.Added++
			if c.Parent == "" && !c.Deleted {
				added = append(added, c)
			}
			continue
		}
		ours := merged[i]
		switch {
		case ours.Deleted:
		case theirs.Deleted:
			merged[i] = theirs.clone()
			res.Deleted++
		case theirs.LastChange().After(ours.LastChange()):
			c := theirs.clone()
			if other.Document != b.Document && ours.Anchor != nil {
				// Their anchor points into their document; ours into ours.
				a := *ours.Anchor
				c.Anchor = &a
			}
			merged[i] = c
			res.Updated++
		}
	}
	b.Comments = merged
	if err := b.Validate(); err != nil {
		b.Comments = before
		return MergeResult{}, err
	}
	if other.Document != b.Document && len(added) > 0 {
		res.Relocations = relocate(b.Document, added)
	}
	return res, nil
}

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }
