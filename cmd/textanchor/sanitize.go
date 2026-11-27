package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// sanitize makes untrusted text safe to write to a terminal. It keeps tab and
// newline, and replaces every other control character, and the bidirectional
// formatting characters, with a visible escape. Invalid UTF-8 becomes U+FFFD.
//
// All text that comes from a bundle must pass through sanitize before output.
func sanitize(s string) string {
	if isClean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case unsafeRune(r):
			fmt.Fprintf(&b, `\u{%04x}`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isClean(s string) bool {
	for _, r := range s {
		if r == utf8.RuneError {
			return false
		}
		if r < 0x20 && r != '\t' && r != '\n' {
			return false
		}
		if r == 0x7f || unsafeRune(r) {
			return false
		}
	}
	return true
}

// unsafeRune reports C1 controls, format characters (Unicode category Cf:
// bidirectional controls, zero-width and other invisible characters) and line
// or paragraph separators. Each can make the displayed text differ from the
// stored text. The zero-width joiner U+200D is allowed, because emoji
// sequences need it.
func unsafeRune(r rune) bool {
	switch {
	case r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x200d:
		return false
	case r == 0x2028 || r == 0x2029:
		return true
	}
	return unicode.Is(unicode.Cf, r)
}

// singleLine sanitizes s and folds it onto one line, for names and previews.
func singleLine(s string) string {
	s = sanitize(s)
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}
