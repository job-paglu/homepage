// Package auth owns administrator identity: password hashing, server-side
// sessions, CSRF, and the middleware that guards every /api/admin route.
//
// The state lives in Postgres rather than in a token, so revoking a session is a
// DELETE and not a denylist (DECISIONS D-8).
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
)

// Params are the Argon2id cost parameters.
//
// 64 MiB / t=3 / p=2 follows the OWASP recommendation and takes roughly 50-100 ms
// on a small single-vCPU instance — slow enough to make offline cracking
// expensive, fast enough that a login is not a page load. The memory cost is the
// part that matters: it is what makes GPU and ASIC attacks uneconomic, which
// bcrypt cannot offer.
type Params struct {
	Memory      uint32 // KiB
	Time        uint32 // passes
	Parallelism uint8  // lanes
	SaltLen     uint32 // bytes
	KeyLen      uint32 // bytes
}

// DefaultParams is what new hashes are written with.
var DefaultParams = Params{
	Memory:      64 * 1024,
	Time:        3,
	Parallelism: 2,
	SaltLen:     16,
	KeyLen:      32,
}

// MinPasswordLen is enforced by adminctl when setting a password. It is not
// enforced on login: rejecting a short password at the login prompt would tell
// an attacker that the stored one is longer than what they tried.
const MinPasswordLen = 12

// ErrInvalidHash means the stored string is not a PHC-encoded Argon2id hash.
// A verify against such a value is an error, never a silent false — a corrupted
// hash column must be visible, not indistinguishable from a wrong password.
var ErrInvalidHash = errors.New("not a valid argon2id PHC hash")

// phcPrefix is the algorithm identifier and version, fixed for everything this
// package writes.
const phcPrefix = "$argon2id$v=19"

// Hash returns a PHC-encoded Argon2id hash of password using DefaultParams.
func Hash(password string) (string, error) {
	return HashWithParams(password, DefaultParams)
}

// HashWithParams hashes with explicit parameters.
//
// The parameters are encoded into the output, which is the entire point of the
// PHC format: DefaultParams can be raised later — as hardware gets faster — and
// every existing hash still verifies against the parameters it was created with.
// No forced password reset, no version column.
func HashWithParams(password string, p Params) (string, error) {
	if p.SaltLen == 0 || p.KeyLen == 0 || p.Memory == 0 || p.Time == 0 || p.Parallelism == 0 {
		return "", fmt.Errorf("argon2id: refusing to hash with zeroed parameters %+v", p)
	}

	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: reading salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, p.KeyLen)

	// PHC uses standard base64 with the padding stripped.
	return fmt.Sprintf("%s$m=%d,t=%d,p=%d$%s$%s",
		phcPrefix, p.Memory, p.Time, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// Verify reports whether password matches the encoded hash.
//
// It returns an error only for a malformed hash. A wrong password is
// (false, nil), because the caller's control flow for "wrong password" and
// "corrupt database row" must not be the same.
func Verify(password, encoded string) (bool, error) {
	p, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}

	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Parallelism, uint32(len(want)))

	// Constant time: a byte-by-byte comparison leaks how much of the hash
	// matched, which is a practical oracle over enough attempts.
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// decodeHash parses `$argon2id$v=19$m=...,t=...,p=...$salt$hash`.
func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// A leading "$" produces an empty first element, so a well-formed string has
	// exactly six: "", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash.
	if len(parts) != 6 || parts[0] != "" {
		return Params{}, nil, nil, fmt.Errorf("%w: expected 5 fields, got %d", ErrInvalidHash, len(parts)-1)
	}
	if parts[1] != "argon2id" {
		return Params{}, nil, nil, fmt.Errorf("%w: algorithm is %q, not argon2id", ErrInvalidHash, parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable version %q", ErrInvalidHash, parts[2])
	}
	if version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("%w: version %d is not supported", ErrInvalidHash, version)
	}

	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Parallelism); err != nil {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable parameters %q", ErrInvalidHash, parts[3])
	}
	if p.Memory == 0 || p.Time == 0 || p.Parallelism == 0 {
		return Params{}, nil, nil, fmt.Errorf("%w: zeroed parameters in %q", ErrInvalidHash, parts[3])
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable salt", ErrInvalidHash)
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Params{}, nil, nil, fmt.Errorf("%w: unreadable hash", ErrInvalidHash)
	}

	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(key))
	return p, salt, key, nil
}

// dummyHash is a hash of a random string nobody knows, computed once.
//
// It exists so an unknown email costs the same as a known one. Without it the
// login handler returns immediately for an address that does not exist, and the
// difference — tens of milliseconds against a sub-millisecond database miss — is
// a reliable oracle for enumerating administrator accounts.
//
// Computed lazily rather than at startup so a cold start is not delayed by a
// 64 MiB KDF pass that most processes never need.
var dummyHash = sync.OnceValue(func() string {
	filler := make([]byte, 32)
	if _, err := rand.Read(filler); err != nil {
		// Any valid hash serves the purpose; the password is never used.
		filler = []byte("argon2id-timing-equaliser-fallback")
	}
	encoded, err := Hash(base64.RawStdEncoding.EncodeToString(filler))
	if err != nil {
		return ""
	}
	return encoded
})

// VerifyDummy runs a full Argon2id pass and discards the result.
//
// Call it on the "no such account" path so the response time matches the
// "wrong password" path. The compiler cannot elide it because Verify's work
// reaches a package-level sink.
func VerifyDummy(password string) {
	encoded := dummyHash()
	if encoded == "" {
		return
	}
	matched, err := Verify(password, encoded)
	dummySink.Store(matched && err == nil)
}

// dummySink keeps VerifyDummy's result observable so no optimiser can decide the
// hashing was dead code. Atomic because concurrent logins reach it, and a plain
// bool here would be a data race the -race detector would rightly flag.
var dummySink atomic.Bool
