# Review bundle format, version 1

A review bundle carries one Markdown document and the comments made on it, so
that people can review a document with different tools and pass it on. The
format is deliberately small: any tool that can read a zip file and JSON can
read and write it.

The recommended file extension is `.mdreview`. The media type, where one is
needed, is `application/vnd.textanchor.review+zip`.

## Layout

A bundle is a zip archive with these entries at its root:

| Entry | Required | Content |
| --- | --- | --- |
| `manifest.json` | yes | format identifier and version |
| `document.md` | yes | the Markdown document: UTF-8, no byte order mark, LF line endings |
| `comments.json` | yes | the comments, UTF-8 JSON |

Other entries are allowed. Readers must ignore entries they do not know.
Writers should copy them unchanged when they rewrite a bundle. Such entries may
go stale when the document changes.

Entry names must not repeat, also when compared without case and after removing
a leading `./`. Entries must not be encrypted, and must use the store or deflate
method.

Writers convert CRLF line endings to LF and remove a byte order mark before they
store a document, so that every tool sees the same bytes.

## manifest.json

```json
{
  "format": "textanchor-review",
  "version": 1
}
```

A reader must reject a bundle whose `format` differs. A reader must reject a
bundle whose `version` is higher than the highest version it supports, because
a newer version may change the meaning of existing fields. Additions that do
not change meaning do not raise the version.

## comments.json

```json
{
  "comments": [
    {
      "id": "3f9a1c2e5d8b7a60",
      "author": "Ada",
      "created": "2026-11-27T14:02:11Z",
      "body": "Is this still true after the 2.0 release?",
      "anchor": {
        "quote": "supports every platform",
        "prefix": "The library ",
        "suffix": " that Go targets.",
        "containing_sentence": "The library supports every platform that Go targets.",
        "heading_context": "Install",
        "paragraph_index": 1
      }
    },
    {
      "id": "b07d44e19c2f3a85",
      "parent": "3f9a1c2e5d8b7a60",
      "author": "Grace",
      "created": "2026-11-27T15:40:00Z",
      "body": "Yes, I checked."
    }
  ]
}
```

A **comment** starts a thread and has an `anchor`. A **reply** has a `parent`
and no `anchor`. Replies attach to the comment that starts the thread, not to
other replies.

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `id` | string | yes | unique within the bundle; 1 to 64 characters from `A-Z a-z 0-9 - _` |
| `parent` | string | replies only | `id` of the comment that starts the thread |
| `author` | string | yes | display name; not verified |
| `created` | string | yes | RFC 3339 timestamp |
| `updated` | string | no | RFC 3339 timestamp of the last change to `body`, `resolved` or `deleted` |
| `body` | string | yes | the comment text, plain text or Markdown |
| `resolved` | boolean | no | the thread is closed; only on comments, default `false` |
| `deleted` | boolean | no | the comment was deleted, see below; default `false` |
| `anchor` | object | comments only | where the comment points, see below |

Writers create ids from at least 64 random bits, for example 16 hexadecimal
digits. Two reviewers then never pick the same id, which makes merging safe.

Readers must ignore fields they do not know. Writers should keep them when they
rewrite a comment.

Comments are listed in the order they were created. Readers may sort them for
display.

### Deleting and merging

A deleted comment stays in the file as a **tombstone**: `deleted` is `true`,
`body` is empty and `anchor` may be omitted. Deleting a comment that starts a
thread also deletes its replies. Readers do not show tombstones.

Tombstones make merging possible. When reviewers send back separate copies of
a bundle, a tool merges them by taking the union of comments by `id`. When both
copies hold the same `id`, a tombstone wins; otherwise the copy with the later
`updated` (or `created`, if `updated` is absent) wins. Merging requires both
copies to hold the same document; otherwise the tool merges the comments into
one copy and re-anchors them as described under "Editing the document".

### Anchor

The anchor is a [textanchor](https://pkg.go.dev/github.com/vloothuis/textanchor)
anchor. It describes a span of `document.md` by its text and its surroundings,
not by an offset, so it survives edits to the document.

| Field | Meaning |
| --- | --- |
| `quote` | the exact text of the span in `document.md`; must not be empty |
| `prefix` | up to about 50 bytes of `document.md` before the span |
| `suffix` | up to about 50 bytes of `document.md` after the span |
| `containing_sentence` | the sentence around a short quote |
| `heading_context` | the text of the nearest heading before the span |
| `paragraph_index` | paragraph number within that heading's section, from 0; absent if unknown |

All text fields hold Markdown source, not rendered text. A tool that lets users
select rendered text must map the selection back to the source before it writes
an anchor.

Only `quote` is required. A tool without the textanchor library can create an
anchor from `quote`, `prefix` and `suffix` alone, and can locate it by searching
for `quote` and preferring the occurrence whose surroundings match `prefix` and
`suffix` best.

When a tool cannot locate an anchor in the current document, the comment is
**orphaned**. Tools must still show orphaned comments, with their `quote`, and
must not delete them on their own.

### Editing the document

A tool that changes `document.md` should locate each anchor in the new text.
When it finds the quote unchanged, or finds a changed quote with high
confidence, it should write a fresh anchor, so that later edits start from
current context. Otherwise it must leave the anchor unchanged: rewriting it
from a weak match would lose the original text for good. The reference
implementation rewrites a changed quote only at a confidence of 0.9 or more.

## Limits

Readers may reject bundles that exceed limits they set to protect themselves.
The reference implementation rejects a bundle when:

- the archive holds more than 1,000 entries,
- `document.md` or `comments.json` exceeds 16 MiB uncompressed,
- `manifest.json` exceeds 64 KiB,
- unknown entries exceed 64 MiB compressed, in total,
- there are more than 10,000 comments and replies,
- a `body` exceeds 64 KiB, an `author` 1 KiB or an `id` 64 characters, or
- an anchor's `quote` exceeds 16 KiB, or one of its other fields 4 KiB.

## Security

A bundle comes from other people. Treat all of its content as untrusted:

- Do not use entry names as file paths.
- Limit how much you decompress.
- Remove or escape control characters, including terminal escape sequences and
  bidirectional overrides, before you display text.
- `author` is a claim, not an identity. It is also personal data that travels
  with the file.
