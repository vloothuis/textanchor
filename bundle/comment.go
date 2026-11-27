package bundle

import (
	"encoding/json"
	"maps"
	"time"

	"github.com/vloothuis/textanchor"
)

// Comment is a comment that starts a thread, or a reply to one.
type Comment struct {
	ID string
	// Parent is the id of the thread's first comment. It is empty for a
	// comment that starts a thread.
	Parent  string
	Author  string
	Created time.Time
	// Updated is when Body, Resolved or Deleted last changed; zero if never.
	Updated  time.Time
	Body     string
	Resolved bool
	// Deleted marks a tombstone. Tombstones are kept so that merging copies of
	// a bundle does not bring deleted comments back.
	Deleted bool
	// Anchor locates the comment in the document. It is nil for replies and
	// may be nil for tombstones.
	Anchor *textanchor.Anchor

	// extra holds fields this package does not know, so they survive a
	// rewrite.
	extra map[string]json.RawMessage
}

// IsReply reports whether c is a reply.
func (c *Comment) IsReply() bool { return c.Parent != "" }

// LastChange returns Updated, or Created if the comment never changed.
func (c *Comment) LastChange() time.Time {
	if c.Updated.IsZero() {
		return c.Created
	}
	return c.Updated
}

func (c *Comment) touch(now time.Time) { c.Updated = now.UTC().Truncate(time.Second) }

// SetBody changes the body and records the change.
func (c *Comment) SetBody(body string) {
	c.Body = body
	c.touch(time.Now())
}

// SetResolved opens or closes the thread and records the change.
func (c *Comment) SetResolved(resolved bool) {
	c.Resolved = resolved
	c.touch(time.Now())
}

// commentJSON is the wire form. It is separate from Comment so that the
// format does not change when the Go types do.
type commentJSON struct {
	ID       string      `json:"id"`
	Parent   string      `json:"parent,omitempty"`
	Author   string      `json:"author"`
	Created  time.Time   `json:"created"`
	Updated  *time.Time  `json:"updated,omitempty"`
	Body     string      `json:"body"`
	Resolved bool        `json:"resolved,omitempty"`
	Deleted  bool        `json:"deleted,omitempty"`
	Anchor   *anchorJSON `json:"anchor,omitempty"`
}

var knownFields = []string{"id", "parent", "author", "created", "updated", "body", "resolved", "deleted", "anchor"}

// anchorJSON is the wire form of a textanchor.Anchor. It does not reuse the
// library's tags: paragraph_index 0 is a real index, so "unknown" is written
// as an absent field rather than as -1 or an omitted zero.
type anchorJSON struct {
	Quote              string `json:"quote"`
	Prefix             string `json:"prefix,omitempty"`
	Suffix             string `json:"suffix,omitempty"`
	ContainingSentence string `json:"containing_sentence,omitempty"`
	HeadingContext     string `json:"heading_context,omitempty"`
	ParagraphIndex     *int   `json:"paragraph_index,omitempty"`
}

func toAnchorJSON(a *textanchor.Anchor) *anchorJSON {
	if a == nil {
		return nil
	}
	j := &anchorJSON{
		Quote:              a.Quote,
		Prefix:             a.Prefix,
		Suffix:             a.Suffix,
		ContainingSentence: a.ContainingSentence,
		HeadingContext:     a.HeadingContext,
	}
	if a.ParagraphIndex >= 0 {
		p := a.ParagraphIndex
		j.ParagraphIndex = &p
	}
	return j
}

func (j *anchorJSON) anchor() *textanchor.Anchor {
	if j == nil {
		return nil
	}
	a := &textanchor.Anchor{
		Quote:              j.Quote,
		Prefix:             j.Prefix,
		Suffix:             j.Suffix,
		ContainingSentence: j.ContainingSentence,
		HeadingContext:     j.HeadingContext,
		ParagraphIndex:     -1,
	}
	if j.ParagraphIndex != nil && *j.ParagraphIndex >= 0 {
		a.ParagraphIndex = *j.ParagraphIndex
	}
	return a
}

// MarshalJSON writes the known fields and any unknown fields read earlier.
func (c *Comment) MarshalJSON() ([]byte, error) {
	j := commentJSON{
		ID:       c.ID,
		Parent:   c.Parent,
		Author:   c.Author,
		Created:  c.Created.UTC(),
		Body:     c.Body,
		Resolved: c.Resolved,
		Deleted:  c.Deleted,
		Anchor:   toAnchorJSON(c.Anchor),
	}
	if !c.Updated.IsZero() {
		t := c.Updated.UTC()
		j.Updated = &t
	}
	known, err := json.Marshal(j)
	if err != nil || len(c.extra) == 0 {
		return known, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(known, &fields); err != nil {
		return nil, err
	}
	merged := maps.Clone(c.extra)
	maps.Copy(merged, fields)
	return json.Marshal(merged)
}

// UnmarshalJSON reads the known fields and keeps the rest.
func (c *Comment) UnmarshalJSON(data []byte) error {
	var j commentJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	for _, k := range knownFields {
		delete(all, k)
	}
	*c = Comment{
		ID:       j.ID,
		Parent:   j.Parent,
		Author:   j.Author,
		Created:  j.Created,
		Body:     j.Body,
		Resolved: j.Resolved,
		Deleted:  j.Deleted,
		Anchor:   j.Anchor.anchor(),
	}
	if j.Updated != nil {
		c.Updated = *j.Updated
	}
	if len(all) > 0 {
		c.extra = all
	}
	return nil
}

func (c *Comment) clone() *Comment {
	d := *c
	if c.Anchor != nil {
		a := *c.Anchor
		d.Anchor = &a
	}
	d.extra = maps.Clone(c.extra)
	return &d
}
