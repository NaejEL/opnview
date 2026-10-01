package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

// testParams are low enough that hashing does not dominate a test run. Nothing in the
// product lowers the defaults.
func testParams() Params {
	return Params{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
}

// TestTheStoredPasswordContainsThePasswordNowhere is AC6's first clause.
func TestTheStoredPasswordContainsThePasswordNowhere(t *testing.T) {
	password := randomValue(t)
	stored, err := HashPassword(password, testParams())
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	if stored.Algorithm != AlgorithmArgon2id {
		t.Errorf("the record names the algorithm %q rather than Argon2id", stored.Algorithm)
	}
	for name, field := range map[string][]byte{
		"the digest": stored.Digest,
		"the salt":   stored.Salt,
	} {
		if bytes.Contains(field, []byte(password)) {
			t.Errorf("%s contains the password", name)
		}
	}
	if len(stored.Salt) < 16 {
		t.Errorf("the salt is %d bytes", len(stored.Salt))
	}
	if len(stored.Digest) < 16 {
		t.Errorf("the digest is %d bytes", len(stored.Digest))
	}
}

// TestTheSaltIsPerAccount: two accounts with the same password must not produce the
// same digest, or one cracked digest is every account with that password.
func TestTheSaltIsPerAccount(t *testing.T) {
	password := randomValue(t)
	first, err := HashPassword(password, testParams())
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	second, err := HashPassword(password, testParams())
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	if bytes.Equal(first.Salt, second.Salt) {
		t.Fatal("two records share a salt")
	}
	if bytes.Equal(first.Digest, second.Digest) {
		t.Error("two records with the same password produced the same digest")
	}
}

// TestVerificationReadsTheParametersFromTheRecord is AC6's second clause, and it is
// the whole of what ROADMAP.md means by "stored beside the hash so they can be raised
// later".
//
// THE DEFAULTS ARE RAISED MID-TEST, on purpose. A record written under the lower
// parameters still verifies afterwards, because verification reads the record. An
// implementation that verified against the defaults would fail here, and it would fail
// in production on the day somebody raised them — silently, as an inability to sign
// in.
func TestVerificationReadsTheParametersFromTheRecord(t *testing.T) {
	low := Params{MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}
	password := randomValue(t)

	written, err := HashPassword(password, low)
	if err != nil {
		t.Fatalf("hashing under the low parameters: %v", err)
	}
	if written.MemoryKiB != low.MemoryKiB || written.Iterations != low.Iterations ||
		written.Parallelism != low.Parallelism {
		t.Fatal("the record does not carry the parameters it was written under")
	}

	// Raise the product's defaults. The record was written before this.
	original := DefaultParams
	t.Cleanup(func() { DefaultParams = original })
	DefaultParams = Params{
		MemoryKiB: low.MemoryKiB * 4, Iterations: low.Iterations + 2,
		Parallelism: low.Parallelism + 1, SaltLength: 32, KeyLength: 32,
	}

	if err := VerifyPassword(written, password); err != nil {
		t.Fatalf("a record written under the old parameters no longer verifies: %v", err)
	}
	if err := VerifyPassword(written, password+"x"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("a wrong password reported %v rather than ErrWrongPassword", err)
	}

	// And a record written now carries the new parameters, so raising them has
	// actually taken effect for what comes next.
	fresh, err := HashPassword(password, DefaultParams)
	if err != nil {
		t.Fatalf("hashing under the raised parameters: %v", err)
	}
	if fresh.Iterations == written.Iterations {
		t.Error("a new record was written under the old parameters")
	}
	if err := VerifyPassword(fresh, password); err != nil {
		t.Errorf("a record written under the raised parameters does not verify: %v", err)
	}
}

// TestAnUnusableRecordFailsRatherThanFallingBack: verifying against parameters a
// record does not carry would verify the wrong thing, so it is a failure.
func TestAnUnusableRecordFailsRatherThanFallingBack(t *testing.T) {
	password := randomValue(t)
	valid, err := HashPassword(password, testParams())
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}

	wrongAlgorithm := valid
	wrongAlgorithm.Algorithm = "bcrypt"
	if err := VerifyPassword(wrongAlgorithm, password); !errors.Is(err, ErrUnknownPasswordAlgorithm) {
		t.Errorf("an unknown algorithm reported %v", err)
	}

	noParameters := valid
	noParameters.Iterations = 0
	if err := VerifyPassword(noParameters, password); !errors.Is(err, ErrInvalidPasswordRecord) {
		t.Errorf("a record with no iteration count reported %v", err)
	}

	noSalt := valid
	noSalt.Salt = nil
	if err := VerifyPassword(noSalt, password); !errors.Is(err, ErrInvalidPasswordRecord) {
		t.Errorf("a record with no salt reported %v", err)
	}
}

// TestAnEmptyPasswordIsRefused: an account whose password is the empty string is an
// account with no password.
func TestAnEmptyPasswordIsRefused(t *testing.T) {
	if _, err := HashPassword("", testParams()); !errors.Is(err, ErrEmptyPassword) {
		t.Errorf("an empty password reported %v", err)
	}
}

// TestUnusableParametersAreRefused: a parameter set that cannot produce a record is a
// refusal rather than a quiet substitution.
func TestUnusableParametersAreRefused(t *testing.T) {
	password := randomValue(t)
	for name, params := range map[string]Params{
		"no memory":      {MemoryKiB: 0, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		"no iterations":  {MemoryKiB: 1024, Iterations: 0, Parallelism: 1, SaltLength: 16, KeyLength: 32},
		"no lanes":       {MemoryKiB: 1024, Iterations: 1, Parallelism: 0, SaltLength: 16, KeyLength: 32},
		"a short salt":   {MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLength: 8, KeyLength: 32},
		"a short digest": {MemoryKiB: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 8},
	} {
		if _, err := HashPassword(password, params); !errors.Is(err, ErrInvalidPasswordRecord) {
			t.Errorf("%s reported %v", name, err)
		}
	}
}

// TestTheProductDefaultsAreTheCitedOnes pins RFC 9106's second recommended option, so
// lowering them is a deliberate act rather than an edit nobody notices.
func TestTheProductDefaultsAreTheCitedOnes(t *testing.T) {
	if DefaultParams.MemoryKiB != 64*1024 {
		t.Errorf("the default memory cost is %d KiB rather than 64 MiB", DefaultParams.MemoryKiB)
	}
	if DefaultParams.Iterations != 3 {
		t.Errorf("the default time cost is %d rather than 3", DefaultParams.Iterations)
	}
	if DefaultParams.Parallelism != 4 {
		t.Errorf("the default lane count is %d rather than 4", DefaultParams.Parallelism)
	}
	if DefaultParams.SaltLength < 16 {
		t.Errorf("the default salt is %d bytes", DefaultParams.SaltLength)
	}
}

// randomValue generates a password for one test run, so none exists in the repository.
func randomValue(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test value: %v", err)
	}
	return hex.EncodeToString(raw)
}
