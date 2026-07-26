package vyosconfig

import (
	"errors"
	"strconv"
	"strings"

	"github.com/GehirnInc/crypt"
	"github.com/GehirnInc/crypt/sha512_crypt"
)

// CryptResult is the tri-state answer to "does this plaintext produce
// this hash?". The unknown case is deliberately not folded into
// "mismatch": an unverifiable hash must not be treated as drift, or a
// deploy would rewrite a password it cannot reason about.
type CryptResult int

const (
	// CryptUnknown means the hash uses a scheme we cannot verify.
	CryptUnknown CryptResult = iota
	// CryptMatch means the plaintext produces the hash.
	CryptMatch
	// CryptMismatch means the plaintext does not produce the hash.
	CryptMismatch
)

// VerifyCryptHash reports whether password matches a unix crypt hash.
//
// Only `$6$` (sha512-crypt) hashes can be checked; anything else — and
// any malformed `$6$` hash — comes back CryptUnknown, which callers must
// treat as "unknown", not as a mismatch. Mirrors Python
// `verify_crypt_hash`, whose None return means the same thing.
func VerifyCryptHash(password, hashed string) CryptResult {
	// The validation must happen before crypt is touched: it is what
	// keeps an absurd `rounds=` out of the hash function, which would
	// otherwise spin for minutes computing a hash nobody wants.
	if !strings.HasPrefix(hashed, "$6$") || !wellFormedSHA512Crypt(hashed) {
		return CryptUnknown
	}
	// Same sha512-crypt implementation the render package hashes with.
	err := sha512_crypt.New().Verify(hashed, []byte(password))
	switch {
	case err == nil:
		return CryptMatch
	case errors.Is(err, crypt.ErrKeyMismatch):
		return CryptMismatch
	default:
		// Malformed salt/rounds: Python's passlib raises ValueError here
		// and the port returns None.
		return CryptUnknown
	}
}

// The shape constraints passlib enforces on a sha512-crypt hash. Values
// come from passlib.handlers.sha2_crypt.sha512_crypt: checksum_size=86,
// max_salt_size=16, salt_chars=HASH64_CHARS, min_rounds=1000,
// max_rounds=999999999.
const (
	sha512CryptChecksumLen = 86
	sha512CryptMaxSaltLen  = 16
	sha512CryptMinRounds   = 1000
	sha512CryptMaxRounds   = 999999999
)

// wellFormedSHA512Crypt reports whether hashed has the shape
// `$6$[rounds=N$]<salt>$<86-char checksum>` *and* stays inside the ranges
// passlib accepts.
//
// This is the guard passlib gives us for free: it raises ValueError on a
// malformed `$6$` string, which the Python port turns into None. The Go
// crypt library instead happily hashes against a truncated hash and
// reports a mismatch — which would make barf treat an unparseable device
// hash as a changed password and rewrite it. Unknown must stay unknown.
//
// The range checks are not cosmetic. Shape-only validation let three
// classes of hash through that Python calls unknown — an out-of-range
// rounds=, an over-long salt, a salt with characters outside the hash64
// alphabet — and each came back CryptMismatch, i.e. "rewrite this live
// router's password". Worse, `$6$rounds=100000000$...` is well-shaped:
// handing it to the crypt library burns minutes of CPU actually
// computing 100M rounds. Rounds are therefore parsed and range-checked
// *before* VerifyCryptHash calls crypt at all, so that hang is not
// reachable.
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
		n, err := strconv.ParseUint(rounds, 10, 64)
		// passlib parses the digits itself: a non-numeric, empty or
		// signed value is a ValueError, as is one outside the range.
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

// wellFormedSalt reports whether salt is at most 16 characters drawn from
// passlib's HASH64_CHARS alphabet, `[./0-9A-Za-z]`.
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
// against the device's stored hashes.
//
// VyOS hashes `plaintext-password` into `encrypted-password` on commit,
// so the two sides of a diff never match textually. For each candidate
// plaintext with an encrypted counterpart on the device:
//
//   - hash matches the plaintext: the password is unchanged; the
//     candidate path is rewritten to the device's, so no diff shows (and
//     the password is not needlessly rewritten on deploy).
//   - hash does not match: the device path is rewritten to the
//     candidate's node name, so the diff pairs them as a replacement.
//   - hash scheme unknown: both sides are left alone (the plaintext shows
//     as an addition rather than being silently swallowed).
//
// The inputs are not modified; adjusted copies of (running, candidate)
// are returned.
func ReconcileHashedPasswords(running, candidate Set) (Set, Set) {
	running = running.Clone()
	candidate = candidate.Clone()

	for _, path := range candidate.Sorted() {
		if len(path) < 2 || path[len(path)-2] != "plaintext-password" {
			continue
		}
		prefix := path[:len(path)-2]
		node := append(prefix.Clone(), "encrypted-password")

		// Snapshot: the loop body rewrites `running`, and Python's list
		// comprehension is likewise evaluated before any mutation.
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
