package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const doc = "# Title\n\nThe library supports every platform that Go targets.\n\n## Risks\n\nAnchors may drift.\n"

func mustAdd(t *testing.T, b *Bundle, quote, body string) *Comment {
	t.Helper()
	i := strings.Index(b.Document, quote)
	if i < 0 {
		t.Fatalf("quote %q not in document", quote)
	}
	c, err := b.AddComment(i, i+len(quote), "Ada", body)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func roundTrip(t *testing.T, b *Bundle) *Bundle {
	t.Helper()
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := Read(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// zipOf builds an archive from name/content pairs, in order.
func zipOf(t *testing.T, entries ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i < len(entries); i += 2 {
		w, err := zw.Create(entries[i])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(entries[i+1]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const manifest = `{"format":"textanchor-review","version":1}`

func readBytes(data []byte) (*Bundle, error) {
	return Read(bytes.NewReader(data), int64(len(data)))
}

func TestRoundTrip(t *testing.T) {
	b := New(doc)
	c := mustAdd(t, b, "supports every platform", "Still true?")
	r, err := b.AddReply(c, "Grace", "Yes.")
	if err != nil {
		t.Fatal(err)
	}
	got := roundTrip(t, b)
	if got.Document != doc {
		t.Errorf("document changed: %q", got.Document)
	}
	if len(got.Comments) != 2 {
		t.Fatalf("got %d comments", len(got.Comments))
	}
	gc := got.Get(c.ID)
	if gc.Body != "Still true?" || gc.Anchor.Quote != "supports every platform" || !gc.Created.Equal(c.Created) {
		t.Errorf("comment changed: %+v", gc)
	}
	if gr := got.Get(r.ID); gr.Parent != c.ID || gr.Anchor != nil {
		t.Errorf("reply changed: %+v", gr)
	}
	if len(c.ID) != 16 {
		t.Errorf("id %q is not 64 bits of hex", c.ID)
	}
}

func TestParagraphIndexWireForm(t *testing.T) {
	b := New(doc)
	c := mustAdd(t, b, "The library", "first paragraph")
	if c.Anchor.ParagraphIndex != 0 {
		t.Fatalf("paragraph index = %d, want 0", c.Anchor.ParagraphIndex)
	}
	data, _ := json.Marshal(c)
	if !strings.Contains(string(data), `"paragraph_index":0`) {
		t.Errorf("index 0 not written: %s", data)
	}

	var d Comment
	if err := json.Unmarshal([]byte(`{"id":"a","author":"x","created":"2026-01-01T00:00:00Z","body":"b","anchor":{"quote":"q"}}`), &d); err != nil {
		t.Fatal(err)
	}
	if d.Anchor.ParagraphIndex != -1 {
		t.Errorf("absent paragraph_index read as %d, want -1", d.Anchor.ParagraphIndex)
	}
	data, _ = json.Marshal(&d)
	if strings.Contains(string(data), "paragraph_index") {
		t.Errorf("unknown index written: %s", data)
	}
}

func TestUnknownDataSurvives(t *testing.T) {
	comments := `{"comments":[{"id":"a1","author":"x","created":"2026-01-01T00:00:00Z","body":"b",
		"anchor":{"quote":"Title"},"color":"red"}],"theme":{"dark":true}}`
	data := zipOf(t,
		ManifestName, `{"format":"textanchor-review","version":1,"generator":"editor 3"}`,
		DocumentName, doc,
		CommentsName, comments,
		"attachments/a.png", "PNGDATA",
	)
	b, err := readBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	b.Comments[0].SetBody("changed")
	got := roundTrip(t, b)

	var buf bytes.Buffer
	got.Write(&buf)
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	all := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		var s bytes.Buffer
		s.ReadFrom(rc)
		rc.Close()
		all[f.Name] = s.String()
	}
	for name, want := range map[string]string{
		ManifestName:        `"generator": "editor 3"`,
		CommentsName:        `"color": "red"`,
		"attachments/a.png": "PNGDATA",
	} {
		if !strings.Contains(all[name], want) {
			t.Errorf("%s lost %s:\n%s", name, want, all[name])
		}
	}
	if !strings.Contains(all[CommentsName], `"theme"`) || !strings.Contains(all[CommentsName], `"changed"`) {
		t.Errorf("comments.json: %s", all[CommentsName])
	}
}

func TestReadRejects(t *testing.T) {
	ok := `{"comments":[]}`
	good := func(c string) []string { return []string{ManifestName, manifest, DocumentName, doc, CommentsName, c} }
	comment := func(fields string) string {
		return `{"comments":[` + fields + `]}`
	}
	base := `"author":"x","created":"2026-01-01T00:00:00Z","body":"b"`
	tests := []struct {
		name    string
		entries []string
		want    string
	}{
		{"missing document", []string{ManifestName, manifest, CommentsName, ok}, "missing document.md"},
		{"wrong format", []string{ManifestName, `{"format":"other","version":1}`, DocumentName, doc, CommentsName, ok}, "format is"},
		{"newer version", []string{ManifestName, `{"format":"textanchor-review","version":2}`, DocumentName, doc, CommentsName, ok}, "version 2"},
		{"duplicate", append(good(ok), DocumentName, "other"), "same name"},
		{"case duplicate", append(good(ok), "Document.MD", "other"), "same name"},
		{"dot duplicate", append(good(ok), "./document.md", "other"), "same name"},
		{"backslash duplicate", append(good(ok), `.\document.md`, "other"), "same name"},
		{"bad utf8", []string{ManifestName, manifest, DocumentName, "\xff", CommentsName, ok}, "UTF-8"},
		{"no comments key", good(`{}`), `missing "comments"`},
		{"bad id", good(comment(`{"id":"a b",` + base + `,"anchor":{"quote":"q"}}`)), "invalid id"},
		{"duplicate id", good(comment(`{"id":"a",` + base + `,"anchor":{"quote":"q"}},{"id":"a",` + base + `,"anchor":{"quote":"q"}}`)), "duplicate id"},
		{"no anchor", good(comment(`{"id":"a",` + base + `}`)), "no anchor"},
		{"empty quote", good(comment(`{"id":"a",` + base + `,"anchor":{"quote":""}}`)), "empty quote"},
		{"orphan reply", good(comment(`{"id":"a","parent":"zz",` + base + `}`)), "does not exist"},
		{"reply with anchor", good(comment(`{"id":"a",` + base + `,"anchor":{"quote":"q"}},{"id":"b","parent":"a",` + base + `,"anchor":{"quote":"q"}}`)), "reply has an anchor"},
		{"nested reply", good(comment(`{"id":"a",` + base + `,"anchor":{"quote":"q"}},{"id":"b","parent":"a",` + base + `},{"id":"c","parent":"b",` + base + `}`)), "itself a reply"},
		{"no author", good(comment(`{"id":"a","created":"2026-01-01T00:00:00Z","body":"b","anchor":{"quote":"q"}}`)), "missing author"},
		{"long quote", good(comment(`{"id":"a",` + base + `,"anchor":{"quote":"` + strings.Repeat("q", MaxQuote+1) + `"}}`)), "quote is larger"},
		{"long prefix", good(comment(`{"id":"a",` + base + `,"anchor":{"quote":"q","prefix":"` + strings.Repeat("p", MaxContext+1) + `"}}`)), "prefix is larger"},
		{"long body", good(comment(`{"id":"a","author":"x","created":"2026-01-01T00:00:00Z","body":"` + strings.Repeat("b", MaxBody+1) + `","anchor":{"quote":"q"}}`)), "body is larger"},
		{"long author", good(comment(`{"id":"a","author":"` + strings.Repeat("x", 2000) + `","created":"2026-01-01T00:00:00Z","body":"b","anchor":{"quote":"q"}}`)), "author is too long"},
		{"too many comments", good(comment(manyComments(MaxComments + 1))), "limit is"},
		{"no created", good(comment(`{"id":"a","author":"x","body":"b","anchor":{"quote":"q"}}`)), "missing created"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := readBytes(zipOf(t, tt.entries...))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func manyComments(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf(`{"id":"c%d","author":"x","created":"2026-01-01T00:00:00Z","body":"b","anchor":{"quote":"q"}}`, i)
	}
	return strings.Join(parts, ",")
}

func TestEntryLimits(t *testing.T) {
	entries := []string{ManifestName, manifest, DocumentName, doc, CommentsName, `{"comments":[]}`}
	for i := 0; i < MaxEntries; i++ {
		entries = append(entries, fmt.Sprintf("extra/%d", i), "")
	}
	if _, err := readBytes(zipOf(t, entries...)); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("entry count: %v", err)
	}

	// Unknown entries are kept raw; a stored entry's compressed size is its
	// full size, so this exceeds the cap.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range [][2]string{{ManifestName, manifest}, {DocumentName, doc}, {CommentsName, `{"comments":[]}`}} {
		w, _ := zw.Create(e[0])
		w.Write([]byte(e[1]))
	}
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "big.bin", Method: zip.Store})
	w.Write(make([]byte, MaxOtherEntries+1))
	zw.Close()
	if _, err := readBytes(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "unknown entries") {
		t.Errorf("unknown entry size: %v", err)
	}
}

func TestUnsupportedMethodRejected(t *testing.T) {
	data := zipOf(t, ManifestName, manifest, DocumentName, doc, CommentsName, `{"comments":[]}`)
	// Set the method field (offset 10) of the first central directory header.
	i := bytes.Index(data, []byte("PK\x01\x02"))
	data[i+10] = 99
	if _, err := readBytes(data); err == nil || !strings.Contains(err.Error(), "compression method") {
		t.Errorf("err = %v", err)
	}
}

func TestTombstoneWithoutAnchorIsValid(t *testing.T) {
	c := `{"comments":[{"id":"a","author":"x","created":"2026-01-01T00:00:00Z","body":"","deleted":true}]}`
	b, err := readBytes(zipOf(t, ManifestName, manifest, DocumentName, doc, CommentsName, c))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Threads()) != 0 {
		t.Error("tombstone listed as a thread")
	}
}

func TestDecompressionLimit(t *testing.T) {
	// Highly compressible: a few KiB on disk, more than MaxDocument unpacked.
	big := strings.Repeat("a", MaxDocument+1)
	data := zipOf(t, ManifestName, manifest, DocumentName, big, CommentsName, `{"comments":[]}`)
	if len(data) > 1<<20 {
		t.Fatalf("test archive is %d bytes; expected compression", len(data))
	}
	_, err := readBytes(data)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err = %v", err)
	}
}

func TestEncryptedEntryRejected(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range [][2]string{{ManifestName, manifest}, {DocumentName, doc}, {CommentsName, `{"comments":[]}`}} {
		h := &zip.FileHeader{Name: e[0], Method: zip.Store}
		if e[0] == DocumentName {
			h.Flags |= 0x1
		}
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(e[1]))
	}
	zw.Close()
	_, err := readBytes(buf.Bytes())
	if err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("err = %v", err)
	}
}

func TestNormalizeDocument(t *testing.T) {
	b := New("\ufeffa\r\nb\r\n")
	if b.Document != "a\nb\n" {
		t.Errorf("got %q", b.Document)
	}
}

func TestDeleteTombstonesThread(t *testing.T) {
	b := New(doc)
	c := mustAdd(t, b, "Title", "x")
	r, _ := b.AddReply(c, "Grace", "y")
	other := mustAdd(t, b, "drift", "z")
	b.Delete(c)
	if !c.Deleted || !r.Deleted || c.Body != "" || c.Anchor != nil {
		t.Errorf("thread not tombstoned: %+v %+v", c, r)
	}
	if other.Deleted {
		t.Error("unrelated comment deleted")
	}
	got := roundTrip(t, b)
	if len(got.Comments) != 3 || len(got.Threads()) != 1 {
		t.Errorf("got %d comments, %d threads", len(got.Comments), len(got.Threads()))
	}
	if _, err := got.Find(c.ID, 4); err == nil {
		t.Error("found a deleted comment")
	}
}

func TestFind(t *testing.T) {
	b := New(doc)
	b.Comments = []*Comment{
		{ID: "abcd1111", Author: "x", Created: time.Now(), Anchor: mustAdd(t, New(doc), "Title", "x").Anchor},
		{ID: "abcd2222", Author: "x", Created: time.Now(), Anchor: mustAdd(t, New(doc), "Title", "x").Anchor},
	}
	if c, err := b.Find("abcd1", 4); err != nil || c.ID != "abcd1111" {
		t.Errorf("Find(abcd1) = %v, %v", c, err)
	}
	if _, err := b.Find("abcd", 4); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("ambiguous prefix: %v", err)
	}
	if _, err := b.Find("abc", 4); err == nil || !strings.Contains(err.Error(), "too short") {
		t.Errorf("short prefix: %v", err)
	}
	if _, err := b.Find("ffff", 4); err == nil {
		t.Error("found a missing id")
	}
}

func TestSetDocument(t *testing.T) {
	b := New(doc)
	kept := mustAdd(t, b, "supports every platform", "unchanged quote, new context")
	fuzzy := mustAdd(t, b, "Anchors may drift.", "rewritten")
	gone := mustAdd(t, b, "## Risks", "removed")
	oldFuzzy := *fuzzy.Anchor

	edited := strings.NewReplacer(
		"The library supports", "Today the library supports",
		"Anchors may drift.", "Anchors might drift a bit.",
		"## Risks\n\n", "",
	).Replace(doc)
	rel := b.SetDocument(edited)
	if b.Document != edited {
		t.Fatal("document not replaced")
	}
	byID := map[string]Relocation{}
	for _, r := range rel {
		byID[r.Comment.ID] = r
	}
	if r := byID[kept.ID]; !r.Reanchored || !strings.Contains(kept.Anchor.Prefix, "Today") {
		t.Errorf("unchanged quote not re-anchored: %+v, prefix %q", r.Result, kept.Anchor.Prefix)
	}
	if r := byID[fuzzy.ID]; r.Reanchored || *fuzzy.Anchor != oldFuzzy {
		t.Errorf("weak match rewrote the anchor: %+v -> %+v", r.Result, fuzzy.Anchor)
	}
	if r := byID[gone.ID]; r.Reanchored || gone.Anchor.Quote != "## Risks" {
		t.Errorf("orphan rewritten: %+v", r)
	}
}

func TestMerge(t *testing.T) {
	base := New(doc)
	shared := mustAdd(t, base, "Title", "original")
	toDelete := mustAdd(t, base, "drift", "will be deleted")
	shared.Created = shared.Created.Add(-time.Hour)
	toDelete.Created = toDelete.Created.Add(-time.Hour)

	ours := roundTrip(t, base)
	theirs := roundTrip(t, base)

	theirs.Get(shared.ID).Body = "their edit"
	theirs.Get(shared.ID).Updated = time.Now().Add(time.Minute)
	theirs.Delete(theirs.Get(toDelete.ID))
	added := mustAdd(t, theirs, "supports", "new from them")
	ours.Get(toDelete.ID).Body = "our late edit"
	ours.Get(toDelete.ID).Updated = time.Now().Add(time.Hour)

	res, err := ours.Merge(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Updated != 1 || res.Deleted != 1 {
		t.Errorf("result = %+v", res)
	}
	if ours.Get(shared.ID).Body != "their edit" {
		t.Error("later edit lost")
	}
	if !ours.Get(toDelete.ID).Deleted {
		t.Error("tombstone did not win over a later edit")
	}
	if ours.Get(added.ID) == nil {
		t.Error("new comment not added")
	}
	ours.Get(added.ID).Body = "changed after merge"
	if theirs.Get(added.ID).Body != "new from them" {
		t.Error("merge shares comments with its input")
	}

	// Merging again changes nothing.
	res, err = ours.Merge(theirs)
	if err != nil || res.Added+res.Updated+res.Deleted != 0 {
		t.Errorf("second merge: %+v, %v", res, err)
	}
}

func TestMergeDifferentDocumentReanchors(t *testing.T) {
	ours := New(strings.Replace(doc, "The library", "Our library", 1))
	theirs := New(doc)
	c := mustAdd(t, theirs, "supports every platform", "x")
	res, err := ours.Merge(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Relocations) != 1 || !res.Relocations[0].Reanchored {
		t.Fatalf("relocations = %+v", res.Relocations)
	}
	if got := ours.Get(c.ID).Anchor.Prefix; !strings.Contains(got, "Our library") {
		t.Errorf("anchor not moved to our document: prefix %q", got)
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.mdreview")
	b := New(doc)
	if err := b.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.mdreview")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	mustAdd(t, b, "Title", "x")
	if err := b.Save(link); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink replaced by a file")
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
	got, err := Open(path)
	if err != nil || len(got.Comments) != 1 {
		t.Fatalf("Open: %v, %d comments", err, len(got.Comments))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("temporary file left behind: %v", entries)
	}
}
