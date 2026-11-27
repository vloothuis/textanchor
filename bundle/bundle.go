// Package bundle reads and writes review bundles: zip archives that hold one
// Markdown document and the comments made on it.
//
// The format is specified in docs/bundle-format.md in this repository. A
// bundle comes from other people, so [Read] validates everything and applies
// size limits before it returns.
package bundle

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Format identifies the bundle format in manifest.json.
const Format = "textanchor-review"

// Version is the highest format version this package reads and the version it
// writes.
const Version = 1

// Entry names inside the archive.
const (
	ManifestName = "manifest.json"
	DocumentName = "document.md"
	CommentsName = "comments.json"
)

// Limits applied by [Read]. A bundle that exceeds one is rejected.
const (
	MaxEntries     = 1000
	MaxDocument    = 16 << 20
	MaxCommentsLen = 16 << 20
	MaxManifest    = 64 << 10
	MaxComments    = 10000
	MaxBody        = 64 << 10
	// MaxOtherEntries caps the total compressed size of entries this package
	// does not know. They are kept in memory to copy on write.
	MaxOtherEntries = 64 << 20
	// MaxFile caps the size of a file passed to [Open].
	MaxFile = MaxDocument + MaxCommentsLen + MaxManifest + MaxOtherEntries

	maxIDLen     = 64
	maxAuthorLen = 1 << 10
)

// Bundle is a document and its comments.
type Bundle struct {
	// Document is the Markdown source, with LF line endings.
	Document string
	// Comments holds comments, replies and tombstones in creation order.
	Comments []*Comment

	manifest  map[string]json.RawMessage
	fileExtra map[string]json.RawMessage
	others    []rawEntry
}

// rawEntry is an entry this package does not know, kept compressed.
type rawEntry struct {
	header zip.FileHeader
	data   []byte
}

// New returns a bundle for document with no comments.
func New(document string) *Bundle {
	return &Bundle{Document: NormalizeDocument(document)}
}

// NormalizeDocument removes a leading byte order mark and converts CRLF line
// endings to LF, as the format requires.
func NormalizeDocument(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// Open reads the bundle at path. The file is closed before Open returns.
func Open(name string) (*Bundle, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFile {
		return nil, fmt.Errorf("%s: file is larger than %d bytes", name, MaxFile)
	}
	b, err := Read(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
}

// Read parses a bundle from r. It keeps no reference to r.
func Read(r io.ReaderAt, size int64) (*Bundle, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a zip archive: %w", err)
	}
	if len(zr.File) > MaxEntries {
		return nil, fmt.Errorf("archive has %d entries; the limit is %d", len(zr.File), MaxEntries)
	}

	known := map[string]*zip.File{}
	seen := map[string]string{}
	b := &Bundle{}
	var otherSize int64
	for _, f := range zr.File {
		key := strings.ToLower(path.Clean("/" + strings.ReplaceAll(f.Name, `\`, "/")))
		if prev, ok := seen[key]; ok {
			return nil, fmt.Errorf("entries %q and %q have the same name", prev, f.Name)
		}
		seen[key] = f.Name
		if f.Flags&0x1 != 0 {
			return nil, fmt.Errorf("entry %q is encrypted", f.Name)
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, fmt.Errorf("entry %q uses unsupported compression method %d", f.Name, f.Method)
		}
		switch f.Name {
		case ManifestName, DocumentName, CommentsName:
			known[f.Name] = f
			continue
		}
		raw, err := readRaw(f, MaxOtherEntries-otherSize)
		if err != nil {
			return nil, err
		}
		otherSize += int64(len(raw))
		b.others = append(b.others, rawEntry{header: f.FileHeader, data: raw})
	}
	for _, name := range []string{ManifestName, DocumentName, CommentsName} {
		if known[name] == nil {
			return nil, fmt.Errorf("missing %s", name)
		}
	}

	manifest, err := readEntry(known[ManifestName], MaxManifest)
	if err != nil {
		return nil, err
	}
	if err := b.parseManifest(manifest); err != nil {
		return nil, err
	}

	doc, err := readEntry(known[DocumentName], MaxDocument)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(doc) {
		return nil, fmt.Errorf("%s is not valid UTF-8", DocumentName)
	}
	b.Document = NormalizeDocument(string(doc))

	comments, err := readEntry(known[CommentsName], MaxCommentsLen)
	if err != nil {
		return nil, err
	}
	if err := b.parseComments(comments); err != nil {
		return nil, err
	}
	return b, nil
}

// readEntry decompresses f, failing once more than limit bytes come out. The
// size in the zip header is not trusted.
func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	defer rc.Close()
	return readLimited(f.Name, rc, limit)
}

// readRaw reads the compressed bytes of f.
func readRaw(f *zip.File, limit int64) ([]byte, error) {
	r, err := f.OpenRaw()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Name, err)
	}
	data, err := readLimited(f.Name, r, limit)
	if err != nil {
		return nil, fmt.Errorf("%w (limit for unknown entries)", err)
	}
	return data, nil
}

func readLimited(name string, r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, limit)
	}
	return data, nil
}

func (b *Bundle) parseManifest(data []byte) error {
	var m struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("%s: %w", ManifestName, err)
	}
	if m.Format != Format {
		return fmt.Errorf("%s: format is %q, want %q", ManifestName, m.Format, Format)
	}
	if m.Version < 1 {
		return fmt.Errorf("%s: invalid version %d", ManifestName, m.Version)
	}
	if m.Version > Version {
		return fmt.Errorf("bundle uses format version %d; this tool supports up to %d", m.Version, Version)
	}
	if err := json.Unmarshal(data, &b.manifest); err != nil {
		return fmt.Errorf("%s: %w", ManifestName, err)
	}
	delete(b.manifest, "format")
	delete(b.manifest, "version")
	return nil
}

func (b *Bundle) parseComments(data []byte) error {
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("%s: %w", CommentsName, err)
	}
	raw, ok := file["comments"]
	if !ok {
		return fmt.Errorf("%s: missing \"comments\"", CommentsName)
	}
	delete(file, "comments")
	b.fileExtra = file
	if err := json.Unmarshal(raw, &b.Comments); err != nil {
		return fmt.Errorf("%s: %w", CommentsName, err)
	}
	if err := b.Validate(); err != nil {
		return fmt.Errorf("%s: %w", CommentsName, err)
	}
	return nil
}

// Validate checks the comments against the format rules.
func (b *Bundle) Validate() error {
	if len(b.Comments) > MaxComments {
		return fmt.Errorf("%d comments; the limit is %d", len(b.Comments), MaxComments)
	}
	byID := make(map[string]*Comment, len(b.Comments))
	for i, c := range b.Comments {
		if c == nil {
			return fmt.Errorf("comment %d is null", i)
		}
		if !validID(c.ID) {
			return fmt.Errorf("comment %d: invalid id %q", i, c.ID)
		}
		if byID[c.ID] != nil {
			return fmt.Errorf("duplicate id %q", c.ID)
		}
		byID[c.ID] = c
	}
	for _, c := range b.Comments {
		if err := c.validate(byID); err != nil {
			return fmt.Errorf("comment %s: %w", c.ID, err)
		}
	}
	return nil
}

func (c *Comment) validate(byID map[string]*Comment) error {
	if c.Author == "" {
		return errors.New("missing author")
	}
	if len(c.Author) > maxAuthorLen {
		return errors.New("author is too long")
	}
	if c.Created.IsZero() {
		return errors.New("missing created")
	}
	if len(c.Body) > MaxBody {
		return fmt.Errorf("body is larger than %d bytes", MaxBody)
	}
	if !utf8.ValidString(c.Author) || !utf8.ValidString(c.Body) {
		return errors.New("text is not valid UTF-8")
	}
	if c.Parent == "" {
		if c.Anchor == nil {
			if c.Deleted {
				return nil
			}
			return errors.New("comment has no anchor")
		}
		if c.Anchor.Quote == "" {
			return errors.New("anchor has an empty quote")
		}
		if !utf8.ValidString(c.Anchor.Quote) {
			return errors.New("anchor quote is not valid UTF-8")
		}
		return nil
	}
	if c.Anchor != nil {
		return errors.New("reply has an anchor")
	}
	if c.Resolved {
		return errors.New("reply is marked resolved")
	}
	p := byID[c.Parent]
	if p == nil {
		return fmt.Errorf("parent %q does not exist", c.Parent)
	}
	if p.Parent != "" {
		return fmt.Errorf("parent %q is itself a reply", c.Parent)
	}
	return nil
}

func validID(id string) bool {
	if id == "" || len(id) > maxIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		ch := id[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '-', ch == '_':
		default:
			return false
		}
	}
	return true
}

// Write writes the bundle as a zip archive to w.
func (b *Bundle) Write(w io.Writer) error {
	if err := b.Validate(); err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	now := time.Now().UTC()

	manifest := map[string]any{}
	for k, v := range b.manifest {
		manifest[k] = v
	}
	manifest["format"] = Format
	manifest["version"] = Version
	if err := writeJSON(zw, ManifestName, manifest, now); err != nil {
		return err
	}
	if err := writeEntry(zw, DocumentName, []byte(NormalizeDocument(b.Document)), now); err != nil {
		return err
	}
	file := map[string]any{}
	for k, v := range b.fileExtra {
		file[k] = v
	}
	comments := b.Comments
	if comments == nil {
		comments = []*Comment{}
	}
	file["comments"] = comments
	if err := writeJSON(zw, CommentsName, file, now); err != nil {
		return err
	}
	for _, e := range b.others {
		h := e.header
		dst, err := zw.CreateRaw(&h)
		if err != nil {
			return fmt.Errorf("copy %s: %w", h.Name, err)
		}
		if _, err := dst.Write(e.data); err != nil {
			return fmt.Errorf("copy %s: %w", h.Name, err)
		}
	}
	return zw.Close()
}

func writeJSON(zw *zip.Writer, name string, v any, now time.Time) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return writeEntry(zw, name, append(data, '\n'), now)
}

func writeEntry(zw *zip.Writer, name string, data []byte, now time.Time) error {
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// Save writes the bundle to name atomically. It writes a temporary file next
// to the target (following a symlink) and renames it over the target, keeping
// the target's permissions.
func (b *Bundle) Save(name string) error {
	var buf bytes.Buffer
	if err := b.Write(&buf); err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(name); err == nil {
		name = resolved
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(name); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(buf.Bytes())
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}
