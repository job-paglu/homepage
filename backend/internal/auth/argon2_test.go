package auth

import (
	"errors"
	"strings"
	"testing"
)

// Argon2id with 64 MiB is intentionally slow, so this package's tests use a
// cheap parameter set wherever the cost parameters are not the thing under test.
var cheap = Params{Memory: 8 * 1024, Time: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestHashAndVerifyRoundTrip(t *testing.T) {
	const password = "correct horse battery staple"

	encoded, err := HashWithParams(password, cheap)
	if err != nil {
		t.Fatalf("HashWithParams() = %v", err)
	}

	matched, err := Verify(password, encoded)
	if err != nil {
		t.Fatalf("Verify() = %v", err)
	}
	if !matched {
		t.Error("Verify() = false for the correct password")
	}
}

func TestVerifyRejectsWrongPasswords(t *testing.T) {
	encoded, err := HashWithParams("correct horse battery staple", cheap)
	if err != nil {
		t.Fatalf("HashWithParams() = %v", err)
	}

	for _, wrong := range []string{
		"correct horse battery stapl",   // one character short
		"correct horse battery staple ", // trailing space
		"Correct horse battery staple",  // case
		"",
		"totally different",
	} {
		matched, err := Verify(wrong, encoded)
		if err != nil {
			t.Errorf("Verify(%q) returned an error: %v", wrong, err)
		}
		if matched {
			t.Errorf("Verify(%q) = true", wrong)
		}
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	const password = "same password twice"

	first, err := HashWithParams(password, cheap)
	if err != nil {
		t.Fatalf("HashWithParams() = %v", err)
	}
	second, err := HashWithParams(password, cheap)
	if err != nil {
		t.Fatalf("HashWithParams() = %v", err)
	}

	if first == second {
		t.Fatal("two hashes of the same password are identical; the salt is not random")
	}
	// Both must still verify: a random salt is only useful if it round-trips.
	for i, encoded := range []string{first, second} {
		matched, err := Verify(password, encoded)
		if err != nil || !matched {
			t.Errorf("hash %d does not verify: matched=%v err=%v", i, matched, err)
		}
	}
}

func TestEncodingIsPHC(t *testing.T) {
	encoded, err := HashWithParams("x", DefaultParams)
	if err != nil {
		t.Fatalf("HashWithParams() = %v", err)
	}

	const wantPrefix = "$argon2id$v=19$m=65536,t=3,p=2$"
	if !strings.HasPrefix(encoded, wantPrefix) {
		t.Errorf("encoded = %q, want the prefix %q", encoded, wantPrefix)
	}

	fields := strings.Split(encoded, "$")
	if len(fields) != 6 {
		t.Fatalf("encoded has %d $-separated fields, want 6: %q", len(fields), encoded)
	}
	// 16-byte salt and 32-byte key, base64 without padding.
	if len(fields[4]) != 22 {
		t.Errorf("salt field is %d characters, want 22 (16 bytes unpadded)", len(fields[4]))
	}
	if len(fields[5]) != 43 {
		t.Errorf("hash field is %d characters, want 43 (32 bytes unpadded)", len(fields[5]))
	}
	// Padding is checked on the salt and hash fields ONLY, not on the whole
	// string. A PHC encoding legitimately contains '=' in its parameter segment
	// -- `v=19$m=65536,t=3,p=2` -- so `strings.Contains(encoded, "=")` is
	// unconditionally true and the assertion could never pass. Caught by the
	// first real execution of this suite, on the EC2 test host
	// (projects/2026-08-22-job-paglu-e2e-ec2, T-4). The implementation was
	// correct all along: the two length checks above already prove the fields are
	// unpadded base64, since padded encodings of 16 and 32 bytes would be 24 and
	// 44 characters.
	if strings.ContainsRune(fields[4], '=') || strings.ContainsRune(fields[5], '=') {
		t.Errorf("salt or hash field contains base64 padding: salt=%q hash=%q", fields[4], fields[5])
	}
}

// The parameters travel with the hash, so raising DefaultParams later must not
// invalidate anything already stored.
func TestVerifyHonoursTheParametersInTheHash(t *testing.T) {
	weak := Params{Memory: 8 * 1024, Time: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	strong := Params{Memory: 16 * 1024, Time: 2, Parallelism: 2, SaltLen: 16, KeyLen: 32}

	weakHash, err := HashWithParams("legacy password", weak)
	if err != nil {
		t.Fatalf("HashWithParams(weak) = %v", err)
	}
	strongHash, err := HashWithParams("legacy password", strong)
	if err != nil {
		t.Fatalf("HashWithParams(strong) = %v", err)
	}

	for name, encoded := range map[string]string{"weak": weakHash, "strong": strongHash} {
		matched, err := Verify("legacy password", encoded)
		if err != nil {
			t.Errorf("Verify(%s) = %v", name, err)
		}
		if !matched {
			t.Errorf("Verify(%s) = false; old hashes must survive a parameter change", name)
		}
	}

	// A different key length must also round-trip, since Verify derives the
	// length from the stored hash rather than from DefaultParams.
	shortKey := Params{Memory: 8 * 1024, Time: 1, Parallelism: 1, SaltLen: 8, KeyLen: 16}
	shortHash, err := HashWithParams("legacy password", shortKey)
	if err != nil {
		t.Fatalf("HashWithParams(shortKey) = %v", err)
	}
	matched, err := Verify("legacy password", shortHash)
	if err != nil || !matched {
		t.Errorf("Verify(shortKey) matched=%v err=%v", matched, err)
	}
}

// A malformed hash must be an error, never a panic and never a silent false.
// Silent false would make a corrupted column look exactly like a wrong password.
func TestVerifyRejectsMalformedHashes(t *testing.T) {
	malformed := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"not PHC at all", "hunter2"},
		{"bcrypt", "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"},
		{"wrong algorithm", "$argon2i$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGho"},
		{"too few fields", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA"},
		{"too many fields", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA$extra"},
		{"missing leading dollar", "argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"unreadable version", "$argon2id$vNINETEEN$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"unsupported version", "$argon2id$v=16$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"unreadable parameters", "$argon2id$v=19$memory=lots$c2FsdA$aGFzaA"},
		{"zeroed parameters", "$argon2id$v=19$m=0,t=0,p=0$c2FsdA$aGFzaA"},
		{"salt is not base64", "$argon2id$v=19$m=65536,t=3,p=2$!!!!$aGFzaA"},
		{"hash is not base64", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$!!!!"},
		{"empty salt", "$argon2id$v=19$m=65536,t=3,p=2$$aGFzaA"},
		{"empty hash", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$"},
	}

	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("Verify() panicked on %q: %v", tc.encoded, recovered)
				}
			}()

			matched, err := Verify("anything", tc.encoded)
			if err == nil {
				t.Fatalf("Verify(%q) = (%v, nil), want an error", tc.encoded, matched)
			}
			if !errors.Is(err, ErrInvalidHash) {
				t.Errorf("error = %v, want it to wrap ErrInvalidHash", err)
			}
			if matched {
				t.Error("Verify() reported a match on a malformed hash")
			}
		})
	}
}

func TestHashWithParamsRejectsZeroedParameters(t *testing.T) {
	for name, p := range map[string]Params{
		"no memory":      {Memory: 0, Time: 3, Parallelism: 2, SaltLen: 16, KeyLen: 32},
		"no passes":      {Memory: 8192, Time: 0, Parallelism: 2, SaltLen: 16, KeyLen: 32},
		"no parallelism": {Memory: 8192, Time: 3, Parallelism: 0, SaltLen: 16, KeyLen: 32},
		"no salt":        {Memory: 8192, Time: 3, Parallelism: 2, SaltLen: 0, KeyLen: 32},
		"no key":         {Memory: 8192, Time: 3, Parallelism: 2, SaltLen: 16, KeyLen: 0},
	} {
		if _, err := HashWithParams("x", p); err == nil {
			t.Errorf("HashWithParams(%s) succeeded with zeroed parameters", name)
		}
	}
}

// The anti-enumeration path must actually do the work, and must never report a
// match for an arbitrary password.
func TestVerifyDummyDoesWorkAndNeverMatches(t *testing.T) {
	VerifyDummy("first call warms the cached hash")
	VerifyDummy("second call reuses it")

	if dummySink.Load() {
		t.Error("VerifyDummy() reported a match; the fixed hash is guessable")
	}
	if encoded := dummyHash(); !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Errorf("dummyHash() = %q, want a real PHC hash so the timing matches", encoded)
	}
}

func TestDefaultParamsMatchTheDecision(t *testing.T) {
	// DECISIONS D-9 and the build brief both pin these. A change here is a
	// deliberate decision that belongs in the documentation first.
	want := Params{Memory: 65536, Time: 3, Parallelism: 2, SaltLen: 16, KeyLen: 32}
	if DefaultParams != want {
		t.Errorf("DefaultParams = %+v, want %+v", DefaultParams, want)
	}
}
