// Package auth holds password hashing and verification, account creation,
// session issue / validate / revoke, and the one-time setup token.
//
// NONE OF THIS IS A SEAM, and that is a decision rather than an omission.
// ROADMAP.md's modularity rule is a rule about KINDS THAT HAVE SEVERAL
// IMPLEMENTATIONS in a real installation — a lease source, a resolver, a theme, a
// widget. There is one mandated password algorithm, one session store, and the
// storage rule forbids provisioning a second one. A registry here would be
// machinery for a problem nobody has. What ROADMAP.md actually asks for is
// PARAMETER AGILITY — the parameters stored beside the hash so they can be raised
// later — and that is a column layout, which internal/store carries.
//
// NO STRING IN THIS PACKAGE IS USER-VISIBLE. Every failure is a typed error, and
// internal/web maps it to a catalogue key.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"

	"github.com/NaejEL/opnview/internal/store"
)

// AlgorithmArgon2id is the label written into every password record this package
// produces. ROADMAP.md mandates the algorithm; the label is stored so that a
// future one is a second label rather than a second copy of the product.
const AlgorithmArgon2id = "argon2id"

// Params are the Argon2id cost parameters.
//
// THEY ARE A VALUE, NOT A CONSTANT, because they are written into each record and
// read back from it. Raising DefaultParams changes what a NEW record costs; a
// record already written still verifies, against the parameters it carries. That
// is the whole of what "stored beside the hash so they can be raised later"
// means, and a test proves it by verifying a record written with one parameter
// set after the defaults have been raised.
type Params struct {
	// MemoryKiB is Argon2's memory cost, in kibibytes.
	MemoryKiB uint32
	// Iterations is its time cost.
	Iterations uint32
	// Parallelism is its lane count.
	Parallelism uint8
	// SaltLength is how many bytes of salt a new record gets.
	SaltLength uint32
	// KeyLength is how many bytes of derived key a new record stores.
	KeyLength uint32
}

// DefaultParams are RFC 9106's second recommended Argon2id option: 64 MiB of
// memory, three passes and four lanes, with a 16-byte salt and a 32-byte derived
// key.
//
// The citation is the reason these numbers are defensible, and it is named rather
// than paraphrased: RFC 9106, section 4, "Parameter Choice", recommends
// t = 3, p = 4, m = 2^16 KiB as the option for a memory-constrained environment,
// which an unprivileged LXC is. Raising them later is a change to this value and
// to nothing else.
var DefaultParams = Params{
	MemoryKiB:   64 * 1024,
	Iterations:  3,
	Parallelism: 4,
	SaltLength:  16,
	KeyLength:   32,
}

// The password failures.
var (
	// ErrEmptyPassword is returned when there is no password to hash. It is a
	// refusal rather than a hash of nothing: an account whose password is the
	// empty string is an account with no password.
	ErrEmptyPassword = errors.New("auth: the password is empty")
	// ErrWrongPassword is returned when verification fails. The caller must not
	// let a client tell it apart from an unknown login.
	ErrWrongPassword = errors.New("auth: the password does not verify")
	// ErrUnknownPasswordAlgorithm is returned for a stored record whose algorithm
	// label this build does not implement.
	ErrUnknownPasswordAlgorithm = errors.New("auth: the stored password names an algorithm this build does not implement")
	// ErrInvalidPasswordRecord is returned for a stored record whose parameters
	// cannot be used. It is a failure rather than a fallback to the defaults:
	// verifying against parameters the record does not carry would verify the
	// wrong thing.
	ErrInvalidPasswordRecord = errors.New("auth: the stored password record is not usable")
)

// HashPassword derives a new password record under params.
//
// The salt is per account and generated here. The password is never stored,
// never logged and never returned.
func HashPassword(password string, params Params) (store.PasswordHash, error) {
	if password == "" {
		return store.PasswordHash{}, ErrEmptyPassword
	}
	if err := params.validate(); err != nil {
		return store.PasswordHash{}, err
	}
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return store.PasswordHash{}, fmt.Errorf("auth: generating a salt: %w", err)
	}
	return store.PasswordHash{
		Algorithm:   AlgorithmArgon2id,
		MemoryKiB:   params.MemoryKiB,
		Iterations:  params.Iterations,
		Parallelism: params.Parallelism,
		Salt:        salt,
		Digest: argon2.IDKey([]byte(password), salt, params.Iterations,
			params.MemoryKiB, params.Parallelism, params.KeyLength),
	}, nil
}

// VerifyPassword checks a password against a stored record.
//
// IT READS THE PARAMETERS FROM THE RECORD, not from DefaultParams. That is what
// makes raising the defaults safe, and it is the criterion a test asserts
// directly.
//
// The comparison is constant-time.
func VerifyPassword(stored store.PasswordHash, password string) error {
	if stored.Algorithm != AlgorithmArgon2id {
		return fmt.Errorf("%w: %q", ErrUnknownPasswordAlgorithm, stored.Algorithm)
	}
	if stored.MemoryKiB == 0 || stored.Iterations == 0 || stored.Parallelism == 0 ||
		len(stored.Salt) == 0 || len(stored.Digest) == 0 {
		return ErrInvalidPasswordRecord
	}
	candidate := argon2.IDKey([]byte(password), stored.Salt, stored.Iterations,
		stored.MemoryKiB, stored.Parallelism, uint32(len(stored.Digest)))
	if subtle.ConstantTimeCompare(candidate, stored.Digest) != 1 {
		return ErrWrongPassword
	}
	return nil
}

// validate refuses a parameter set that cannot produce a record.
func (p Params) validate() error {
	switch {
	case p.MemoryKiB == 0, p.Iterations == 0, p.Parallelism == 0:
		return fmt.Errorf("%w: a cost parameter is zero", ErrInvalidPasswordRecord)
	case p.SaltLength < 16:
		return fmt.Errorf("%w: the salt would be shorter than 16 bytes", ErrInvalidPasswordRecord)
	case p.KeyLength < 16:
		return fmt.Errorf("%w: the derived key would be shorter than 16 bytes", ErrInvalidPasswordRecord)
	}
	return nil
}
