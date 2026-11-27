package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/vloothuis/textanchor/bundle"
)

// minPrefix is the shortest id prefix the commands accept.
const minPrefix = 4

func cmdInit(e *env, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	out := fs.String("o", "", "output `file` (default: the document name with .mdreview)")
	force := fs.Bool("force", false, "overwrite an existing output file")
	names := "<doc.md> [-o out.mdreview] [--force]"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 1, names)
	if err != nil {
		return err
	}
	doc, err := readDocument(pos[0])
	if err != nil {
		return err
	}
	dst := *out
	if dst == "" {
		dst = strings.TrimSuffix(pos[0], filepath.Ext(pos[0])) + ".mdreview"
	}
	b := bundle.New(doc)
	if err := createFile(dst, *force, b.Write); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Created %s.\n", sanitize(dst))
	return nil
}

func cmdShow(e *env, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	resolved := fs.Bool("resolved", false, "also show resolved threads")
	color := colorFlag(fs)
	names := "<bundle> [--resolved]"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 1, names)
	if err != nil {
		return err
	}
	st, err := newStyle(e, *color)
	if err != nil {
		return err
	}
	b, err := bundle.Open(pos[0])
	if err != nil {
		return err
	}
	show(e.stdout, st, b, locate(b, *resolved))
	return nil
}

func cmdList(e *env, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	resolved := fs.Bool("resolved", false, "also list resolved threads")
	asJSON := fs.Bool("json", false, "print JSON")
	color := colorFlag(fs)
	names := "<bundle> [--resolved] [--json]"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 1, names)
	if err != nil {
		return err
	}
	st, err := newStyle(e, *color)
	if err != nil {
		return err
	}
	b, err := bundle.Open(pos[0])
	if err != nil {
		return err
	}
	threads := locate(b, *resolved)
	if *asJSON {
		return listJSON(e.stdout, b, threads)
	}
	list(e.stdout, st, b, threads)
	return nil
}

func cmdAdd(e *env, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	quote := fs.String("quote", "", "the `text` to comment on, as in the source or as rendered")
	nth := fs.Int("nth", 0, "which occurrence of --quote, from 1")
	lines := fs.String("line", "", "comment on line `N` or lines N-M instead of a quote")
	body := fs.String("m", "", "comment `text`; read from standard input if absent")
	author := authorFlag(fs)
	names := "<bundle> (--quote TEXT [--nth N] | --line N[-M]) [-m BODY]"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 1, names)
	if err != nil {
		return err
	}
	if (*quote == "") == (*lines == "") {
		return usageErr("add: give exactly one of --quote and --line")
	}
	b, err := bundle.Open(pos[0])
	if err != nil {
		return err
	}
	var start, end int
	if *quote != "" {
		start, end, err = findQuote(b.Document, *quote, *nth)
	} else {
		start, end, err = lineRange(b.Document, *lines)
	}
	if err != nil {
		return err
	}
	who, err := resolveAuthor(e, *author)
	if err != nil {
		return err
	}
	text, err := readBody(e, fs, *body)
	if err != nil {
		return err
	}
	c, err := b.AddComment(start, end, who, text)
	if err != nil {
		return err
	}
	if err := b.Save(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Added comment %s on line %d.\n", c.ID, lineOf(b.Document, start))
	return nil
}

func cmdReply(e *env, args []string) error {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	body := fs.String("m", "", "reply `text`; read from standard input if absent")
	author := authorFlag(fs)
	names := "<bundle> <id> [-m BODY]"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 2, names)
	if err != nil {
		return err
	}
	b, c, err := openComment(pos[0], pos[1])
	if err != nil {
		return err
	}
	who, err := resolveAuthor(e, *author)
	if err != nil {
		return err
	}
	text, err := readBody(e, fs, *body)
	if err != nil {
		return err
	}
	r, err := b.AddReply(c, who, text)
	if err != nil {
		return err
	}
	if err := b.Save(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Added reply %s to %s.\n", r.ID, r.Parent)
	return nil
}

func cmdEdit(e *env, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	body := fs.String("m", "", "new `text`; read from standard input if absent")
	names := "<bundle> <id> [-m BODY]"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 2, names)
	if err != nil {
		return err
	}
	b, c, err := openComment(pos[0], pos[1])
	if err != nil {
		return err
	}
	text, err := readBody(e, fs, *body)
	if err != nil {
		return err
	}
	c.SetBody(text)
	if err := b.Save(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Edited %s.\n", c.ID)
	return nil
}

func cmdResolve(e *env, args []string, resolved bool) error {
	name := "resolve"
	if !resolved {
		name = "reopen"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	names := "<bundle> <id>"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 2, names)
	if err != nil {
		return err
	}
	b, c, err := openComment(pos[0], pos[1])
	if err != nil {
		return err
	}
	if c.IsReply() {
		return fmt.Errorf("%s is a reply; %s its thread %s instead", c.ID, name, c.Parent)
	}
	c.SetResolved(resolved)
	if err := b.Save(pos[0]); err != nil {
		return err
	}
	verb := "Resolved"
	if !resolved {
		verb = "Reopened"
	}
	fmt.Fprintf(e.stdout, "%s %s.\n", verb, c.ID)
	return nil
}

func cmdDelete(e *env, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	names := "<bundle> <id>"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 2, names)
	if err != nil {
		return err
	}
	b, c, err := openComment(pos[0], pos[1])
	if err != nil {
		return err
	}
	replies := 0
	if !c.IsReply() {
		replies = len(b.Replies(c.ID))
	}
	b.Delete(c)
	if err := b.Save(pos[0]); err != nil {
		return err
	}
	switch replies {
	case 0:
		fmt.Fprintf(e.stdout, "Deleted %s.\n", c.ID)
	case 1:
		fmt.Fprintf(e.stdout, "Deleted %s and 1 reply.\n", c.ID)
	default:
		fmt.Fprintf(e.stdout, "Deleted %s and %d replies.\n", c.ID, replies)
	}
	return nil
}

func cmdMerge(e *env, args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	names := "<bundle> <other>..."
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, -2, names)
	if err != nil {
		return err
	}
	b, err := bundle.Open(pos[0])
	if err != nil {
		return err
	}
	for _, name := range pos[1:] {
		other, err := bundle.Open(name)
		if err != nil {
			return err
		}
		res, err := b.Merge(other)
		if err != nil {
			return fmt.Errorf("merge %s: %w", name, err)
		}
		fmt.Fprintf(e.stdout, "%s: %d added, %d updated, %d deleted.\n",
			sanitize(name), res.Added, res.Updated, res.Deleted)
		if len(res.Relocations) > 0 {
			fmt.Fprintf(e.stdout, "  The document differs from %s; added comments were matched to it:\n", sanitize(pos[0]))
			reportRelocations(e.stdout, res.Relocations)
		}
	}
	if err := b.Save(pos[0]); err != nil {
		return err
	}
	return nil
}

func cmdExtract(e *env, args []string) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	out := fs.String("o", "", "output `file` (default: standard output)")
	force := fs.Bool("force", false, "overwrite an existing output file")
	names := "<bundle> [-o doc.md] [--force]"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 1, names)
	if err != nil {
		return err
	}
	b, err := bundle.Open(pos[0])
	if err != nil {
		return err
	}
	if *out == "" {
		_, err := io.WriteString(e.stdout, b.Document)
		return err
	}
	return createFile(*out, *force, func(w io.Writer) error {
		_, err := io.WriteString(w, b.Document)
		return err
	})
}

func cmdUpdate(e *env, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	names := "<bundle> <doc.md>"
	helpFlags(e, fs, names)
	pos, err := parse(fs, args, 2, names)
	if err != nil {
		return err
	}
	b, err := bundle.Open(pos[0])
	if err != nil {
		return err
	}
	doc, err := readDocument(pos[1])
	if err != nil {
		return err
	}
	rel := b.SetDocument(doc)
	if err := b.Save(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "Updated the document in %s.\n", sanitize(pos[0]))
	reportRelocations(e.stdout, rel)
	return nil
}

// reportRelocations lists the comments that kept their old anchor.
func reportRelocations(w io.Writer, rel []bundle.Relocation) {
	moved := 0
	for _, r := range rel {
		if r.Reanchored {
			moved++
		}
	}
	fmt.Fprintf(w, "  %d of %d comments matched the new text.\n", moved, len(rel))
	for _, r := range rel {
		if r.Reanchored {
			continue
		}
		state := fmt.Sprintf("uncertain match (%.0f%%), anchor kept", 100*r.Result.Confidence)
		if r.Result.Orphaned {
			state = "not found, anchor kept"
		}
		fmt.Fprintf(w, "  %s %s: %q\n", r.Comment.ID, state, preview(r.Comment.Anchor.Quote, 50))
	}
}

// openComment opens a bundle and finds a live comment by id or prefix.
func openComment(path, id string) (*bundle.Bundle, *bundle.Comment, error) {
	b, err := bundle.Open(path)
	if err != nil {
		return nil, nil, err
	}
	c, err := b.Find(id, minPrefix)
	if err != nil {
		return nil, nil, err
	}
	return b, c, nil
}

func readDocument(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, bundle.MaxDocument+1))
	if err != nil {
		return "", err
	}
	if len(data) > bundle.MaxDocument {
		return "", fmt.Errorf("%s is larger than %d bytes", path, bundle.MaxDocument)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not valid UTF-8", path)
	}
	return string(data), nil
}

// createFile writes a new file. Without force it fails if the file exists.
func createFile(path string, force bool, write func(io.Writer) error) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s exists; use --force to overwrite it", path)
	}
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
