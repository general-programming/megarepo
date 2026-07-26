// Package pytext provides Go equivalents of Python stdlib text primitives,
// each differentially validated against CPython: not "a line splitter" but
// *Python's*, byte-for-byte. A leaf package on purpose — the parent `common`
// pulls in redis and zap.
package pytext

import "unicode/utf8"

// SplitLines splits s the way Python's str.splitlines() does. strings.Split on
// "\n" is NOT equivalent: it keeps CRLF's "\r" and ignores Python's other
// boundaries — LF, CR, CRLF, VT, FF, FS, GS, RS, NEL (U+0085), U+2028, U+2029.
// A trailing boundary adds no final empty element.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isLineBoundary(r) {
			i += size
			continue
		}
		lines = append(lines, s[start:i])
		i += size
		// CRLF is one boundary, not two.
		if r == '\r' && i < len(s) && s[i] == '\n' {
			i++
		}
		start = i
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// isLineBoundary reports whether Python's str.splitlines() breaks on r.
func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
