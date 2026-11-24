//go:build unix

// Package inputguard frames terminal input for Bubble Tea. It hands over
// complete messages and keeps only a trailing incomplete CSI sequence or a
// partial UTF-8 character for the next read. A lone Escape is delivered at
// once: a sequence whose second byte has not arrived is indistinguishable from
// the Escape key, and that ambiguity is left to the caller as a known limit.
package inputguard

import "unicode/utf8"

const esc = 0x1b

// completeLen is the length of the longest prefix that ends on a message
// boundary. A tail inside a CSI sequence or a UTF-8 character is left for the
// next read.
func completeLen(b []byte) int {
	i, done := 0, 0
	for i < len(b) {
		if b[i] != esc {
			r, size := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && size == 1 && !utf8.FullRune(b[i:]) {
				return done
			}
			i += size
			done = i
			continue
		}
		end := escapeEnd(b, i)
		if end < 0 {
			return done
		}
		i, done = end, end
	}
	return done
}

// escapeEnd returns the index just past the escape sequence starting at i, or
// -1 while the bytes seen so far could still grow into one. CSI parameter,
// intermediate and final bytes are kept whole. A sequence without its next
// byte is treated as a key rather than held: ESC alone is Escape, ESC O is
// Alt+O, and ESC plus a rune is Alt+that rune.
func escapeEnd(b []byte, i int) int {
	if i+1 >= len(b) {
		return i + 1
	}
	switch b[i+1] {
	case '[':
		return csiEnd(b, i)
	case 'O':
		if i+2 >= len(b) {
			return i + 2
		}
		// Only an ASCII final belongs to SS3. A multibyte rune after
		// Alt+O is a separate character, not the SS3 final.
		if c := b[i+2]; c < 0x40 || c > 0x7e {
			return i + 2
		}
		return i + 3
	}
	if !utf8.FullRune(b[i+1:]) {
		return -1
	}
	_, size := utf8.DecodeRune(b[i+1:])
	return i + 1 + size
}

// csiEnd returns the end of the CSI sequence at i, or -1 when it is
// incomplete. Parameter bytes run 0x30-0x3f, intermediate bytes 0x20-0x2f,
// and the final byte 0x40-0x7e allows arbitrary sequences such as mode
// reports.
func csiEnd(b []byte, i int) int {
	j := i + 2
	for j < len(b) && b[j] >= 0x30 && b[j] <= 0x3f {
		j++
	}
	for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
		j++
	}
	if j >= len(b) {
		return -1
	}
	c := b[j]
	if c >= 0x40 && c <= 0x7e {
		return j + 1
	}
	// A byte that cannot belong to a CSI ends the sequence before itself,
	// so a following control key is still delivered. The head is handed over
	// as text, which is what Bubble Tea does with an unknown sequence.
	return j
}
