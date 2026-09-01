# textanchor

Durable references to spans of text in Markdown documents — anchors that survive
edits to the document they point into.

[![Go Reference](https://pkg.go.dev/badge/github.com/vloothuis/textanchor.svg)](https://pkg.go.dev/github.com/vloothuis/textanchor)

## The problem

You want to attach something — a comment, a highlight, a suggestion — to a span
of text in a Markdown file, and store it *outside* that file. A byte offset
breaks as soon as anyone inserts a line above it. A plain text search breaks as
soon as the same words appear twice.

`textanchor` describes a span by its content and its surroundings, then relocates
it later by searching and scoring. When it cannot find a confident match, it says
so rather than guessing.

## Install

```bash
go get github.com/vloothuis/textanchor
```

## Usage

```go
import "github.com/vloothuis/textanchor"

// Create an anchor for document[start:end].
a, err := textanchor.New(document, start, end, nil)
if err != nil {
    return err
}
// ... serialise and store `a` alongside your annotation ...

// Later, against a possibly-edited document:
res := textanchor.Resolve(edited, a, nil)
switch {
case res.Orphaned:
    // The text is gone or changed too much. Show a.Quote as context —
    // don't silently drop the annotation.
    log.Printf("orphaned: %s", res.OrphanReason)
default:
    fmt.Println(edited[res.Range.Start:res.Range.End], res.Confidence)
}
```

Resolving many anchors against one document is cheaper in a single call, which
hoists the per-document structural analysis out of the loop:

```go
results := textanchor.ResolveAll(document, anchors, nil)
```

## What an anchor stores

| Field | Purpose |
| --- | --- |
| `Quote` | the exact selected text |
| `Prefix` / `Suffix` | ~50 characters either side, to disambiguate repeats |
| `ContainingSentence` | the full sentence, for quotes under 50 characters |
| `HeadingContext` | the nearest preceding ATX heading |
| `ParagraphIndex` | paragraph index within that heading's section |

The structural fields are what let an anchor survive a rewrite that defeats
quote matching alone.

## How resolution works

1. **Candidates.** Every exact occurrence of `Quote`. If there are none, fall
   back to fuzzy matching (paragraph-scoped, sliding window at ±25% of the quote
   length) with a 0.8 penalty applied to the resulting score.
2. **Scoring.** Each candidate is scored:

   ```
   0.40*quote + 0.20*prefix + 0.20*suffix + 0.10*structural + 0.10*uniqueness
   ```

   where `structural` is itself `0.7*heading + 0.3*paragraph`, and `uniqueness`
   is `1/occurrences` — so a quote appearing once scores higher than one
   appearing five times.
3. **Threshold.** The best candidate wins if it scores at or above
   `MinConfidence` (default `0.5`). Otherwise the result is orphaned, and
   `Confidence` still reports the best score achieved.

Similarity is length-adaptive: Levenshtein ratio below 100 characters,
token-based Jaccard above. Quotes shorter than 5 characters are not fuzzy-matched
at all, since short generic strings match everywhere.

## Selections made over rendered HTML

A browser selection gives you rendered text — no markdown syntax, whitespace
collapsed, possibly spanning several blocks — so its offsets do not correspond to
the source. The `quotefind` subpackage bridges that gap by walking the goldmark
AST and building a per-byte map from rendered text back to source offsets:

```go
import "github.com/vloothuis/textanchor/quotefind"

start, end, ok := quotefind.Find(markdownSource, selectedText)
if ok {
    a, err := textanchor.New(markdownSource, start, end, nil)
    // ...
}
```

Use `quotefind.FindWithContext` when the selection may occur more than once, and
`quotefind.RenderedTextWithMapping` if you need the rendered text and position
map to do your own matching.

## Notes

- Offsets are **byte** offsets, matching Go's string indexing.
- Normalise your documents consistently. If your storage layer reformats
  Markdown (re-wrapping paragraphs, for instance), run the same normalisation
  before creating an anchor and before resolving one, or anchors will orphan on
  a purely cosmetic change.
- Treat orphaning as normal rather than exceptional, and surface orphans to the
  user with `Quote` as context. Research on annotation systems is consistent on
  this point: hiding a failed anchor makes users think their work was lost, and a
  bad guess is worse than an honest "not found".

## Provenance

Extracted from [margin](https://github.com/vloothuis/margin), a CLI for
annotating Markdown without modifying the source files.

## License

MIT — see [LICENSE](LICENSE).
