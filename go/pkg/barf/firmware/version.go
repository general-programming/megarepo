// Package firmware ports barf's image provider and firmware mirror
// (projects/barf/barf/util/images.py, .../firmware.py): the newest image for a
// device type, and where a device downloads it from. Lookups degrade to the
// unknown marker ("?") when upstream is unreachable, as Python does. Nothing
// here writes to a device.
package firmware

import "strings"

// Unknown is the cell value when the latest version cannot be determined;
// Python prints the same "?".
const Unknown = "?"

// IsCurrent reports whether version is running the latest release.
// Deliberately a substring test, not a version comparison, mirroring Python
// VyOSImageProvider.is_current: VyOS prefixes/suffixes its rolling tag.
func IsCurrent(latest, version string) bool {
	if latest == "" || version == "" {
		return false
	}
	return strings.Contains(version, latest)
}

// CompareVersions orders two "YYYY.MM.DD-HHMM-rolling" tags as -1/0/+1; every
// field is fixed-width, so lexicographic order is chronological. Reporting
// only — the is-current test stays IsCurrent's containment check.
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
