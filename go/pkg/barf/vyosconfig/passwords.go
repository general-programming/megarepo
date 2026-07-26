package vyosconfig

import (
	"errors"
	"strconv"
	"strings"

	"github.com/GehirnInc/crypt"
	"github.com/GehirnInc/crypt/sha512_crypt"
)

// CryptResult is the tri-state answer to "does this plaintext produce this
// hash?". Unknown is deliberately not folded into mismatch: an unverifiable
// hash must not read as drift, or a deploy would rewrite it.
type CryptResult int

const (
	// CryptUnknown means the hash uses a scheme we cannot verify.
	CryptUnknown CryptResult = iota
	CryptMatch
	CryptMismatch
)

// VerifyCryptHash reports whether password matches a unix crypt hash. Only
// `$6$` (sha512-crypt) is checkable; anything else, and any malformed `$6$`,
// is CryptUnknown — never a mismatch, matching `verify_crypt_hash`'s None.
func VerifyCryptHash(password, hashed string) CryptResult {
	// Must run first: it keeps an absurd `rounds=` out of the hash function,
	// which would spin for minutes.
	if !strings.HasPrefix(hashed, "$6$") || !wellFormedSHA512Crypt(hashed) {
		return CryptUnknown
	}
	err := sha512_crypt.New().Verify(hashed, []byte(password))
	switch {
	case err == nil:
		return CryptMatch
	case errors.Is(err, crypt.ErrKeyMismatch):
		return CryptMismatch
	default:
		// Malformed salt/rounds: passlib raises ValueError, i.e. None.
		return CryptUnknown
	}
}

// Shape constraints from passlib.handlers.sha2_crypt.sha512_crypt.
const (
	sha512CryptChecksumLen = 86
	sha512CryptMaxSaltLen  = 16
	sha512CryptMinRounds   = 1000
	sha512CryptMaxRounds   = 999999999
)

// wellFormedSHA512Crypt reports whether hashed is
// `$6$[rounds=N$]<salt>$<86-char checksum>` *and* inside passlib's ranges.
//
// Replicates a guard passlib gives Python for free: it raises ValueError on
// a malformed `$6$` (-> None -> unknown), whereas Go's crypt library hashes
// against a truncated hash and reports a mismatch, making barf rewrite an
// unparseable hash. The ranges also keep `$6$rounds=100000000$...`, which is
// well-shaped, from burning minutes of CPU.
func wellFormedSHA512Crypt(hashed string) bool {
	parts := strings.Split(hashed, "$")
	// ["", "6", salt, checksum] or ["", "6", "rounds=N", salt, checksum].
	switch len(parts) {
	case 4:
	case 5:
		rounds, ok := strings.CutPrefix(parts[2], "rounds=")
		if !ok {
			return false
		}
		// passlib: non-numeric, empty, signed or out-of-range is ValueError.
		n, err := strconv.ParseUint(rounds, 10, 64)
		if err != nil || n < sha512CryptMinRounds || n > sha512CryptMaxRounds {
			return false
		}
	default:
		return false
	}
	if !wellFormedSalt(parts[len(parts)-2]) {
		return false
	}
	return len(parts[len(parts)-1]) == sha512CryptChecksumLen
}

// wellFormedSalt reports whether salt is at most 16 chars from passlib's
// HASH64_CHARS alphabet, `[./0-9A-Za-z]`.
func wellFormedSalt(salt string) bool {
	if len(salt) > sha512CryptMaxSaltLen {
		return false
	}
	for i := 0; i < len(salt); i++ {
		c := salt[i]
		switch {
		case c == '.' || c == '/':
		case c >= '0' && c <= '9':
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		default:
			return false
		}
	}
	return true
}

// ReconcileHashedPasswords matches candidate `plaintext-password` paths
// against the device's hashes, returning adjusted copies. VyOS hashes
// plaintext into `encrypted-password` on commit, so the two sides of a diff
// never match textually:
//
//   - match: candidate path rewritten to the device's, so no diff shows.
//   - mismatch: device path rewritten to the candidate's node name, so the
//     diff pairs them as a replacement.
//   - unknown: both left alone, so the plaintext shows as an addition
//     rather than being silently swallowed.
func ReconcileHashedPasswords(running, candidate Set) (Set, Set) {
	running = running.Clone()
	candidate = candidate.Clone()

	for _, path := range candidate.Sorted() {
		if len(path) < 2 || path[len(path)-2] != "plaintext-password" {
			continue
		}
		prefix := path[:len(path)-2]
		node := append(prefix.Clone(), "encrypted-password")

		// Snapshot: the loop body rewrites `running`, as Python's list
		// comprehension is evaluated before mutation.
		var devicePaths []Path
		for _, r := range running.Sorted() {
			if len(r) == len(node)+1 && slicesEqualPath(r[:len(r)-1], node) {
				devicePaths = append(devicePaths, r)
			}
		}

		for _, devicePath := range devicePaths {
			switch VerifyCryptHash(path[len(path)-1], devicePath[len(devicePath)-1]) {
			case CryptMatch:
				candidate.Discard(path)
				candidate.Add(devicePath)
			case CryptMismatch:
				running.Discard(devicePath)
				running.Add(append(prefix.Clone(), "plaintext-password", devicePath[len(devicePath)-1]))
			case CryptUnknown:
				// Leave both sides alone.
			}
		}
	}

	return running, candidate
}

func slicesEqualPath(a, b Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
