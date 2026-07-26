package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// printTable writes the plain-mode table: columns padded to their widest
// cell, joined by two spaces, with a dashed rule under the header. This
// is barf/cli/common.py's print_table, with one deliberate difference —
// trailing padding is trimmed, so piping into grep/awk/diff never turns
// up invisible whitespace.
func printTable(w io.Writer, headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				continue
			}
			if n := utf8.RuneCountInString(cell); n > widths[i] {
				widths[i] = n
			}
		}
	}

	writeRow := func(cells []string) {
		var b strings.Builder
		for i := range headers {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(cell)
			if i < len(headers)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)))
			}
		}
		fmt.Fprintln(w, strings.TrimRight(b.String(), " "))
	}

	writeRow(headers)
	rule := make([]string, len(headers))
	for i, width := range widths {
		rule[i] = strings.Repeat("-", width)
	}
	writeRow(rule)
	for _, row := range rows {
		writeRow(row)
	}
}
