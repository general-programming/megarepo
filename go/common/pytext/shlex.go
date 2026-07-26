package pytext

import (
	"errors"
	"strings"
)

// Port of the two Python `shlex` entry points vyos_config.py needs and Go's
// stdlib lacks: `shlex.split` (POSIX) for rendered `set` lines, `shlex.quote`
// for printing them back. The templates quote inconsistently (`'aes256'` vs
// `aes256`), so normalization must match Python byte for byte or config paths
// stop comparing equal. ShellQuote is also barf/sshx's quoter for remote shell
// lines, a command-injection boundary. Both validated against CPython.

// ErrNoClosingQuote mirrors Python's ValueError("No closing quotation").
var ErrNoClosingQuote = errors.New("no closing quotation")

// ErrNoEscapedCharacter mirrors Python's ValueError("No escaped character").
var ErrNoEscapedCharacter = errors.New("no escaped character")

const (
	shlexWhitespace = " \t\r\n"
	shlexQuotes     = "'\""
	// Only double quotes honour backslash escapes, as in POSIX shells.
	shlexEscapedQuotes = "\""
	shlexEscape        = '\\'
)

// ShellSplit tokenizes s as Python's `shlex.split(s)`: POSIX mode, whitespace
// splitting, comments disabled.
func ShellSplit(s string) ([]string, error) {
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
			// escape char may be escaped; anything else keeps its backslash.
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
		return nil, ErrNoClosingQuote
	case shlexEscape:
		return nil, ErrNoEscapedCharacter
	}
	flush()
	return tokens, nil
}

// ShellQuote is Python's `shlex.quote`: s unchanged when every character is
// shell-safe, otherwise wrapped in single quotes.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsFunc(s, shlexUnsafe) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// shlexUnsafe is Python's `re.compile(r"[^\w@%+=:,./-]", re.ASCII)`: with the
// ASCII flag `\w` is exactly [A-Za-z0-9_], so non-ASCII runes are unsafe too.
func shlexUnsafe(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return false
	}
	return !strings.ContainsRune("_@%+=:,./-", c)
}
