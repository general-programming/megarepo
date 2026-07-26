package vyosconfig

import (
	"errors"
	"strings"
)

// This file is a port of the two Python `shlex` entry points
// projects/barf/barf/util/vyos_config.py relies on: `shlex.split` (POSIX
// mode) for parsing rendered `set` lines, and `shlex.quote` for printing
// them back. Go has neither in its standard library, and the templates'
// inconsistent quoting (`'aes256'` vs `aes256`) means the normalization
// has to match Python's byte for byte or paths stop comparing equal.

// errNoClosingQuote mirrors Python's ValueError("No closing quotation").
var errNoClosingQuote = errors.New("no closing quotation")

// errTrailingEscape mirrors Python's ValueError("No escaped character").
var errTrailingEscape = errors.New("no escaped character")

const (
	shlexWhitespace = " \t\r\n"
	shlexQuotes     = "'\""
	// Only double quotes honour backslash escapes, as in POSIX shells.
	shlexEscapedQuotes = "\""
	shlexEscape        = '\\'
)

// shlexSplit tokenizes s exactly as Python's `shlex.split(s)` does:
// POSIX mode, whitespace splitting, comments disabled.
func shlexSplit(s string) ([]string, error) {
	var (
		tokens  []string
		token   strings.Builder
		started bool
	)

	// state mirrors shlex's: 0 = between tokens, 'a' = in a bare word,
	// '\'' / '"' = inside that quote, '\\' = the char after an escape.
	const stateBetween = 0
	const stateWord = 'a'
	var state rune = stateBetween
	var escapedState rune = stateBetween

	flush := func() {
		if started {
			tokens = append(tokens, token.String())
			token.Reset()
			started = false
		}
	}

	for _, c := range s {
		switch {
		case state == shlexEscape:
			// POSIX: inside a quoted string only the quote itself and the
			// escape char may be escaped; anything else keeps its
			// backslash.
			if strings.ContainsRune(shlexQuotes, escapedState) &&
				c != shlexEscape && c != escapedState {
				token.WriteRune(shlexEscape)
			}
			token.WriteRune(c)
			state = escapedState

		case state == '\'' || state == '"':
			switch {
			case c == state:
				state = stateWord
			case strings.ContainsRune(shlexEscapedQuotes, state) && c == shlexEscape:
				escapedState = state
				state = shlexEscape
			default:
				token.WriteRune(c)
			}

		case strings.ContainsRune(shlexWhitespace, c):
			if state == stateWord {
				flush()
				state = stateBetween
			}

		case strings.ContainsRune(shlexQuotes, c):
			started = true
			state = c

		case c == shlexEscape:
			started = true
			escapedState = stateWord
			state = shlexEscape

		default:
			started = true
			token.WriteRune(c)
			state = stateWord
		}
	}

	switch state {
	case '\'', '"':
		return nil, errNoClosingQuote
	case shlexEscape:
		return nil, errTrailingEscape
	}
	flush()
	return tokens, nil
}

// shlexQuote is Python's `shlex.quote`: return s unchanged when every
// character is shell-safe, otherwise wrap it in single quotes.
func shlexQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsFunc(s, shlexUnsafe) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// shlexUnsafe is Python's `re.compile(r"[^\w@%+=:,./-]", re.ASCII)`:
// with the ASCII flag `\w` is exactly [A-Za-z0-9_], so any non-ASCII rune
// is unsafe too.
func shlexUnsafe(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return false
	}
	return !strings.ContainsRune("_@%+=:,./-", c)
}
