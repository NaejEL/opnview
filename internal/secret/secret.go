// Package secret holds the key file beside the database and the authenticated
// encryption of the credentials opnview has to be able to send again.
//
// WHY ENCRYPTION AND NOT HASHING, stated once here because it is the thing most
// likely to be "corrected" by somebody who knows that passwords are hashed.
// ROADMAP.md, "Rules that apply to every step": the OPNsense secret and the
// MaxMind key are NOT passwords. They are sent to those services on every call,
// so hashing them would make the product unable to work. They are encrypted at
// rest and decrypted to be used. The account password is the other thing, it is
// hashed, and it lives in internal/auth.
//
// WHY THIS IS NOT A SEAM. One AEAD is enough and a registry for it would be
// machinery for a problem nobody has. The mitigation is the same one the password
// record uses: an algorithm LABEL and a KEY IDENTIFIER are stored beside every
// ciphertext, so a future scheme is a second label rather than a second copy of
// the product, and a key that has been replaced is distinguishable from a
// ciphertext that has been tampered with.
//
// THE LIMIT OF WHAT THE KEY FILE PROTECTS is stated in README.md's limitations
// section, in ROADMAP.md's own words. It is not restated on any screen, and no
// string in this package is user-visible.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// KeyFilename is the name of the key file inside the data directory. It sits
// beside the database, which is what ROADMAP.md asks for, and it is never
// written into the database.
const KeyFilename = "opnview.key"

// AlgorithmAESGCM is the label stored beside every ciphertext this package
// produces. AES-256-GCM is in the standard library, so the AEAD adds no
// dependency.
const AlgorithmAESGCM = "aes-256-gcm"

// keyLength is 32 bytes, which is what makes the cipher AES-256.
const keyLength = 32

// keyFileMode denies group and other. The mode is asserted by a test, and that
// assertion is meaningful only inside the Debian container: a Windows host
// cannot express it, and neither the container nor the host says anything about
// the unprivileged-LXC uid mapping, which is step 8.
const keyFileMode fs.FileMode = 0o600

// The failures, each of which is a distinct state rather than one error.
var (
	// ErrNoKeyFile is returned when the data directory holds no key file. With a
	// database that holds ciphertexts, this is the lost-key-file state, and it is
	// NOT the same state as "no firewall is configured".
	ErrNoKeyFile = errors.New("secret: there is no key file in the data directory")
	// ErrMalformedKeyFile is returned when the file exists and does not hold a
	// key. It is deliberately not folded into ErrNoKeyFile: a truncated file is a
	// different accident from a missing one.
	ErrMalformedKeyFile = errors.New("secret: the key file does not hold a key of the expected length")
	// ErrWrongKey is returned when a ciphertext was sealed under a different key
	// than the one now held. The key identifier stored beside the ciphertext is
	// what makes this reportable instead of indistinguishable from tampering.
	ErrWrongKey = errors.New("secret: the stored credential was sealed with a different key")
	// ErrUnknownAlgorithm is returned for a ciphertext whose algorithm label this
	// build does not implement.
	ErrUnknownAlgorithm = errors.New("secret: the stored credential names an algorithm this build does not implement")
	// ErrAuthenticationFailed is returned when the AEAD rejects the ciphertext.
	// It is a FAILURE and never a partial result: nothing is returned beside it.
	ErrAuthenticationFailed = errors.New("secret: the stored credential failed authentication")
)

// Ciphertext is one sealed value with everything needed to open it.
//
// The nonce, the algorithm label and the key identifier are beside the bytes and
// not inside them, because a screen has to be able to say WHY a credential
// cannot be opened without opening it.
type Ciphertext struct {
	// Algorithm is the label of the scheme that sealed the value.
	Algorithm string
	// KeyID identifies the key that sealed it.
	KeyID string
	// Nonce is the nonce it was sealed with. It is generated per seal and never
	// reused.
	Nonce []byte
	// Bytes is the sealed value, authentication tag included.
	Bytes []byte
}

// Box seals and opens values under one key.
type Box struct {
	key   []byte
	keyID string
	aead  cipher.AEAD
}

// KeyPath returns where the key file sits for a data directory.
func KeyPath(dataDir string) string { return filepath.Join(dataDir, KeyFilename) }

// Create generates a key file in dataDir and returns a box over it.
//
// It refuses to overwrite an existing file: O_EXCL is what makes that a
// guarantee rather than a check with a race in it. Losing the key means losing
// the stored credentials, so creating one over another is not something this
// package does by accident.
func Create(dataDir string) (*Box, error) {
	key := make([]byte, keyLength)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("secret: generating a key: %w", err)
	}
	path := KeyPath(dataDir)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyFileMode)
	if err != nil {
		return nil, fmt.Errorf("secret: creating %s: %w", path, err)
	}
	encoded := make([]byte, hex.EncodedLen(len(key))+1)
	hex.Encode(encoded, key)
	encoded[len(encoded)-1] = '\n'
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secret: writing %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("secret: closing %s: %w", path, err)
	}
	// The mode is set again after the write because the process umask is applied
	// to the mode given to OpenFile, and a umask that clears nothing is not
	// something this package is entitled to assume.
	if err := os.Chmod(path, keyFileMode); err != nil {
		return nil, fmt.Errorf("secret: setting the mode of %s: %w", path, err)
	}
	return newBox(key)
}

// Open reads the key file in dataDir and returns a box over it. A missing file
// is ErrNoKeyFile, which is a state the interface names.
func Open(dataDir string) (*Box, error) {
	path := KeyPath(dataDir)
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%w: %s", ErrNoKeyFile, path)
	case err != nil:
		return nil, fmt.Errorf("secret: reading %s: %w", path, err)
	}
	key, err := decodeKey(raw)
	if err != nil {
		return nil, err
	}
	return newBox(key)
}

// OpenOrCreate reads the key file, or creates it if there is none. It is what a
// first start calls, and it is deliberately NOT what the recovery path calls:
// recovery has to be able to tell a missing key file from a present one.
func OpenOrCreate(dataDir string) (*Box, error) {
	box, err := Open(dataDir)
	if err == nil {
		return box, nil
	}
	if !errors.Is(err, ErrNoKeyFile) {
		return nil, err
	}
	return Create(dataDir)
}

// decodeKey reads the on-disk encoding of a key.
//
// The file holds the key hex-encoded with a trailing newline, so it can be read,
// copied and compared by a person who has to present it to the recovery surface —
// which is the one thing a lost password needs it for.
func decodeKey(raw []byte) ([]byte, error) {
	trimmed := make([]byte, 0, len(raw))
	for _, character := range raw {
		switch character {
		case ' ', '\t', '\r', '\n':
		default:
			trimmed = append(trimmed, character)
		}
	}
	if hex.DecodedLen(len(trimmed)) != keyLength {
		return nil, ErrMalformedKeyFile
	}
	key := make([]byte, keyLength)
	if _, err := hex.Decode(key, trimmed); err != nil {
		return nil, ErrMalformedKeyFile
	}
	return key, nil
}

// newBox builds a box over a raw key.
func newBox(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: building the cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: building the AEAD: %w", err)
	}
	return &Box{key: key, keyID: keyIdentifier(key), aead: aead}, nil
}

// keyIdentifier is a short, stable name for a key that reveals nothing about it.
//
// It is the first eight bytes of SHA-256 over the key, hex-encoded. It is stored
// beside every ciphertext so that a key file which has been REPLACED — a
// different key, not a missing one — is reported as such rather than as an
// authentication failure, which would read as tampering.
func keyIdentifier(key []byte) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:8])
}

// KeyID is the identifier of the key this box holds.
func (b *Box) KeyID() string { return b.keyID }

// Matches reports whether presented is the key this box holds.
//
// It is the whole of what "possession of the key file" means for the recovery
// path, and the comparison is constant-time so that a wrong file cannot be
// narrowed down by timing. presented is the FILE CONTENT, in the encoding the
// file uses, so the caller hands over what the user pasted and nothing is parsed
// twice.
func (b *Box) Matches(presented []byte) bool {
	key, err := decodeKey(presented)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, b.key) == 1
}

// Seal encrypts one value under this box's key.
//
// The key identifier is bound into the AEAD's ADDITIONAL DATA as well as stored
// beside the ciphertext, so an attacker who can write to the database cannot
// relabel a ciphertext as having been sealed by another key without the
// authentication failing.
func (b *Box) Seal(plaintext []byte) (Ciphertext, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Ciphertext{}, fmt.Errorf("secret: generating a nonce: %w", err)
	}
	return Ciphertext{
		Algorithm: AlgorithmAESGCM,
		KeyID:     b.keyID,
		Nonce:     nonce,
		Bytes:     b.aead.Seal(nil, nonce, plaintext, b.additionalData()),
	}, nil
}

// Unseal decrypts one value, or fails.
//
// IT NEVER RETURNS A PARTIAL OR PLAUSIBLE RESULT. A tampered ciphertext, a
// truncated one, a wrong nonce and a wrong key are each an error with nothing
// beside it. That is the property AES-GCM is here for and the reason the scheme
// is authenticated encryption rather than a cipher.
func (b *Box) Unseal(sealed Ciphertext) ([]byte, error) {
	if sealed.Algorithm != AlgorithmAESGCM {
		return nil, fmt.Errorf("%w: %q", ErrUnknownAlgorithm, sealed.Algorithm)
	}
	if sealed.KeyID != b.keyID {
		return nil, fmt.Errorf("%w: it names key %q and this installation holds key %q",
			ErrWrongKey, sealed.KeyID, b.keyID)
	}
	if len(sealed.Nonce) != b.aead.NonceSize() {
		return nil, fmt.Errorf("%w: the nonce is %d bytes and this algorithm takes %d",
			ErrAuthenticationFailed, len(sealed.Nonce), b.aead.NonceSize())
	}
	plaintext, err := b.aead.Open(nil, sealed.Nonce, sealed.Bytes, b.additionalData())
	if err != nil {
		return nil, ErrAuthenticationFailed
	}
	return plaintext, nil
}

// additionalData is what the AEAD authenticates without encrypting: the
// algorithm label and the key identifier, which are the two fields stored in the
// clear beside the ciphertext.
func (b *Box) additionalData() []byte {
	return []byte(AlgorithmAESGCM + ":" + b.keyID)
}
