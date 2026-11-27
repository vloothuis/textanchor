# textanchor CLI: design and threat model

## Purpose

`textanchor` is the minimal tool for working with [review bundles](bundle-format.md).
Anyone can use it to read the comments on a document and add their own, without
an editor that supports the format.

## Packages

- `bundle`: reads, validates, merges and writes review bundles, and re-anchors
  comments when the document changes. It has no terminal code, so editors
  written in Go can use it.
- `cmd/textanchor`: the command-line tool.

## Commands

```
textanchor init <doc.md> [-o out.mdreview]     create a bundle from a document
textanchor show <bundle> [--resolved]          print the document with comments
textanchor list <bundle> [--resolved] [--json] print the comments only
textanchor add <bundle> (--quote TEXT [--nth N] | --line N[-M]) [-m BODY]
textanchor reply <bundle> <id> [-m BODY]
textanchor edit <bundle> <id> [-m BODY]
textanchor resolve <bundle> <id>
textanchor reopen <bundle> <id>
textanchor delete <bundle> <id>                delete a comment, or a thread with its replies
textanchor merge <bundle> <other>...           merge comments from other copies into <bundle>
textanchor extract <bundle> [-o doc.md]        write the document out
textanchor update <bundle> <doc.md>            replace the document, re-anchor comments
textanchor version
```

- `<id>` accepts a unique prefix of at least 4 characters.
- Without `-m`, the body is read from standard input. When standard input is a
  terminal, the tool says so first.
- `init -o` and `extract -o` refuse to overwrite a file unless `--force` is given.
- The author comes from `--author`, else `TEXTANCHOR_AUTHOR`, else
  `git config user.name`, else the login name. This name is stored in the file
  and travels with it.
- `--color=auto|always|never` controls colour. `auto` uses colour only when
  standard output is a terminal and `NO_COLOR` is unset.
- `--quote` first searches the source for the exact text. If it is not there,
  it searches the rendered text, so a quote copied from a rendered view works.
  If the quote occurs more than once, the command fails and lists the
  occurrences, unless `--nth` picks one.

`show` prints the document with each commented span highlighted and numbered.
Each thread is printed under the line where its span ends. Orphaned comments are
printed at the end, with their quote. Resolved threads are hidden unless
`--resolved` is given.

## Threat model

### Assets

- The user's terminal session.
- The user's files, including the bundle being edited.
- The user's trust in what the tool shows them.

### Trust boundary

Bundles are made by other people and sent by mail or chat. Everything inside a
bundle is untrusted: entry names, sizes, JSON, the document and the comments.
The command-line arguments and standard input come from the user and are
trusted.

### Threats and mitigations

| # | Threat | Mitigation |
| --- | --- | --- |
| T1 | **Terminal escape injection.** A comment or the document contains ANSI or OSC sequences that rewrite the screen, change the window title, or write to the clipboard (OSC 52). | All untrusted text passes through one sanitiser before output, including error messages. It replaces C0 controls other than tab and newline, DEL, and C1 controls with visible `\xNN` or `\u{NNNN}` forms. The tool emits its own colour codes only around sanitised text. |
| T2 | **Hidden text.** Bidirectional controls, zero-width characters and line separators make displayed text differ from what is stored. | The sanitiser shows them as `\u{NNNN}`. |
| T3 | **Zip bomb.** A small archive expands to gigabytes. | Each known entry is read through a limit reader, and the read fails when the limit is reached. The declared size is not trusted. Unknown entries are kept compressed and copied raw, with a cap on their total compressed size. The number of entries is capped. |
| T4 | **Path traversal (zip slip).** An entry named `../../.bashrc`. | The tool never extracts entries by name. It reads three fixed names. `extract` writes only to the path the user gives, or to standard output; on a terminal that output is sanitised too. |
| T5 | **Ambiguous archive.** Two entries named `document.md`, or `Document.md` and `./document.md`, so different tools show different documents. | Names that repeat after case folding and path cleaning are rejected, as are encrypted entries and methods other than store and deflate. |
| T6 | **Malformed data.** Invalid JSON, invalid UTF-8, missing fields, replies to missing comments, duplicate ids. | Strict validation on read, with a clear error. Invalid UTF-8 in display text is shown as U+FFFD. |
| T7 | **CPU exhaustion.** Many comments with long quotes force expensive fuzzy matching. | Caps on comment count, body size and document size. The library bounds fuzzy matching per anchor. There is no total budget, so a hostile bundle can still be slow; the user can interrupt it. |
| T8 | **Corrupted bundle on write.** A crash or full disk while saving loses the review. | Write to a temporary file in the same directory as the resolved path, sync, then rename over the original, keeping its mode. The source file is closed after reading, so the rename also works on Windows. |
| T9 | **Spoofed authorship.** Anyone can write any `author`. | Documented as a claim. No signatures in version 1. |
| T10 | **Silent loss of comments.** An edit to the document orphans a comment and a tool drops it. | Orphaned comments are always shown. `update` rewrites an anchor only when its quote is unchanged or matched at confidence 0.9 or more, and reports the rest. |
| T11 | **Lost deletions on merge.** A deleted comment returns when copies are merged. | Deletions are tombstones, and tombstones win in a merge. |

### Out of scope

- Confidentiality: bundles are not encrypted.
- Integrity and authenticity: bundles are not signed.
- Concurrent edits of one bundle file by two processes.

## Release

Tags matching `v*` trigger a GitHub Actions workflow that runs the tests, then
GoReleaser. GoReleaser builds `textanchor` for Linux, macOS and Windows on amd64
and arm64, and publishes archives and a checksum file to a GitHub release. The
version is stamped into the binary through `-ldflags`.
