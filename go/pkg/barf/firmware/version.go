// Package firmware is the Go port of barf's image provider and firmware
// mirror (projects/barf/barf/util/images.py and .../firmware.py).
//
// It answers two questions for the rest of barf:
//
//   - "what is the newest image for this device type?", which backs the
//     LATEST FIRMWARE column of `barf status`; and
//   - "where can a device download that image from?", which backs the
//     update flow: the vendor image (plus its detached .minisig) is
//     mirrored into an S3-compatible bucket and the device is handed the
//     public URL.
//
// Everything here degrades gracefully. A status table is still useful
// when GitHub or the mirror is unreachable, so lookups return an unknown
// marker ("?") rather than failing the caller, matching the Python
// implementation which logs a warning and continues.
//
// Nothing in this package writes to a network device.
package firmware

import "strings"

// Unknown is the cell value used when the latest version cannot be
// determined (no provider for the device type, or the upstream release
// feed was unreachable). Python prints the same "?".
const Unknown = "?"

// IsCurrent reports whether a device-reported version string is running
// the latest release.
//
// This deliberately mirrors Python's VyOSImageProvider.is_current, which
// is a substring test (`self.latest_version in version`) and not a
// version comparison: VyOS reports its running image as something like
// "2026.07.21-1151-rolling" but has historically prefixed and suffixed
// that tag, so containment is the check that has always been applied.
func IsCurrent(latest, version string) bool {
	if latest == "" || version == "" {
		return false
	}
	return strings.Contains(version, latest)
}

// CompareVersions orders two VyOS rolling tags of the form
// "YYYY.MM.DD-HHMM-rolling".
//
// That format is fixed-width in every field that matters (four-digit
// year, zero-padded month/day, four-digit HHMM), so a plain lexicographic
// comparison is a correct chronological ordering: the real fleet values
// "2026.07.11-0033-rolling" (running) and "2026.07.21-1151-rolling"
// (latest) differ first at the day field, where '1' < '2'.
//
// Returns -1 when a is older than b, +1 when newer, 0 when equal. It is
// only used for reporting ("is this device behind?"); the actual
// is-current test stays byte-identical to Python's containment check.
func CompareVersions(a, b string) int {
	switch {
	case a == b:
		return 0
	case a < b:
		return -1
	default:
		return 1
	}
}
