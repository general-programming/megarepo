package vyosconfig

import "unicode/utf8"

// splitLines splits s on line boundaries the way Python's
// str.splitlines() does, and is the counterpart every parser in this
// package that ports a Python `for line in text.splitlines()` loop needs.
//
// strings.Split(s, "\n") is not the same function. It leaves CRLF's
// trailing "\r" glued to the end of every line, and it does not break on
// the other separators Python recognises at all. Here that matters for
// ParseSetCommands, which is fed rendered template output that may carry
// CRLF endings.
//
// Boundaries are Python's: LF, CR, CRLF, VT, FF, FS, GS, RS, NEL
// (U+0085), LINE SEPARATOR (U+2028) and PARAGRAPH SEPARATOR (U+2029).
// Like splitlines(), a trailing boundary does not produce a final empty
// element, and "" splits to nothing.
func splitLines(s string) []string {
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

// isLineBoundary reports whether r is one of the code points Python's
// str.splitlines() breaks on.
func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
