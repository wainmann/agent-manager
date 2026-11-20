// Package inputguard frames terminal input for Bubble Tea. Complete messages
// are handed over whole; a broken SGR report is cut at the byte that cannot
// belong to it, a CSI keeps its intermediate bytes, and a short SS3 key or
// half a UTF-8 character waits briefly for its remaining bytes.
package inputguard

import "unicode/utf8"

const esc = 0x1b

// completeLen is the length of the longest prefix that ends on a message
// boundary: after a whole escape sequence or rune. A tail inside a sequence is
// left for the next read.
func completeLen(b []byte) int {
	i, done := 0, 0
	for i < len(b) {
		if b[i] != esc {
			_, size := utf8.DecodeRune(b[i:])
			if size == 1 && !utf8.FullRune(b[i:]) {
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
// -1 while the bytes seen so far could still grow into one. CSI parameter and
// intermediate bytes are kept, so mode reports such as ?2026;1$y stay whole.
// Anything else is an ESC-prefixed key, which is how Bubble Tea reads it too.
func escapeEnd(b []byte, i int) int {
	if i+1 >= len(b) {
		return -1
	}
	switch b[i+1] {
	case '[':
		sgr := i+2 < len(b) && b[i+2] == '<'
		j := i + 2
		for j < len(b) && b[j] >= 0x30 && b[j] <= 0x3f {
			j++
		}
		for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2f {
			if b[j] == '$' && j > i+2 {
				// urxvt ends its Shift+Home/End reports on '$' with no final.
				if params := string(b[i+2 : j]); params == "7" || params == "8" {
					return j + 1
				}
			}
			j++
		}
		if j >= len(b) {
			return -1
		}
		if b[j] < 0x40 || b[j] > 0x7e {
			return -1
		}
		if sgr && b[j] != 'M' && b[j] != 'm' {
			// An SGR report ends only on M or m; anything else is a break
			// the caller turns into a key press.
			return -1
		}
		if b[j] == 'M' && j == i+2 {
			// X10 mouse: the final byte carries three more.
			if j+4 > len(b) {
				return -1
			}
			return j + 4
		}
		return j + 1
	case 'O':
		// SS3 carries one more byte.
		if i+2 >= len(b) {
			return -1
		}
		return i + 3
	}
	// An escape marking alt carries one rune.
	if !utf8.FullRune(b[i+1:]) {
		return -1
	}
	_, size := utf8.DecodeRune(b[i+1:])
	return i + 1 + size
}
