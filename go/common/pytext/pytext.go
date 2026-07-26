// Package pytext provides Go equivalents of Python stdlib text primitives
// whose exact semantics we depend on when porting Python code.
//
// Everything here is domain-free and differentially validated against
// CPython: the point is not "a line splitter" or "a shell quoter" but
// *Python's* line splitter and *Python's* shell quoter, byte-for-byte, so
// that a ported loop or a ported command string behaves identically to the
// implementation it was ported from.
//
// This is a leaf package on purpose. The parent `common` package pulls in
// redis, zap and friends; callers that only need text semantics should not
// inherit that dependency graph.
package pytext

import "unicode/utf8"

// SplitLines splits s on line boundaries the way Python's
// str.splitlines() does, and is the counterpart every parser that ports a
// Python `for line in text.splitlines()` loop needs.
//
// strings.Split(s, "\n") is not the same function. It leaves CRLF's
// trailing "\r" glued to the end of every line, and it does not break on
// the other separators Python recognises at all. Callers that TrimSpace
// each line afterwards hide the difference; the ones that do not, break.
// Concretely, in this repo:
//
//   - barf/device: the `show version` fallback in ParseVyOSVersion returns
//     its line verbatim, so a device answering with CRLF used to yield
//     "1.4.2\r" where Python yields "1.4.2", and that value goes straight
//     into the fleet table and version comparisons.
//   - barf/scope: running-config text arriving over eAPI from a device, or
//     from a capture with CRLF endings, must be seen as the same lines the
//     Python implementation saw, or the scoped comparison reports drift
//     that is not there.
//   - barf/vyosconfig: ParseSetCommands is fed rendered template output
//     that may carry CRLF endings.
//
// Boundaries are Python's: LF, CR, CRLF, VT, FF, FS, GS, RS, NEL
// (U+0085), LINE SEPARATOR (U+2028) and PARAGRAPH SEPARATOR (U+2029).
// Like splitlines(), a trailing boundary does not produce a final empty
// element, and "" splits to nothing.
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

// isLineBoundary reports whether r is one of the code points Python's
// str.splitlines() breaks on.
func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
