package secret

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"testing"
)

// TestTheKeyFileIsCreatedWithPermissionsDenyingGroupAndOther is AC12.
//
// THE MODE ASSERTION IS MEANINGFUL ONLY INSIDE THE CONTAINER, and it says so rather
// than passing quietly on a host that cannot express it. It also proves nothing about
// the unprivileged-LXC uid mapping, which is step 8 and needs a real LXC.
func TestTheKeyFileIsCreatedWithPermissionsDenyingGroupAndOther(t *testing.T) {
	directory := t.TempDir()
	box, err := Create(directory)
	if err != nil {
		t.Fatalf("creating the key file: %v", err)
	}
	if box.KeyID() == "" {
		t.Error("the key has no identifier")
	}

	info, err := os.Stat(KeyPath(directory))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skipf("file modes are not expressible on %s; this criterion is asserted in the "+
			"Debian container, which is where every project command runs", runtime.GOOS)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("the key file is mode %04o, which grants group or other access", mode)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the key file is mode %04o rather than 0600", mode)
	}
}

// TestCreateRefusesToOverwriteAnExistingKeyFile: losing the key means losing the
// stored credentials, so this cannot be something that happens by accident.
func TestCreateRefusesToOverwriteAnExistingKeyFile(t *testing.T) {
	directory := t.TempDir()
	if _, err := Create(directory); err != nil {
		t.Fatalf("creating the key file: %v", err)
	}
	before, err := os.ReadFile(KeyPath(directory))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	if _, err := Create(directory); err == nil {
		t.Error("a second Create overwrote the key file")
	}
	after, err := os.ReadFile(KeyPath(directory))
	if err != nil {
		t.Fatalf("re-reading the key file: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the key file changed")
	}
}

// TestAMissingKeyFileIsItsOwnError is the first half of AC14: the condition has a
// name of its own before anything reports it.
func TestAMissingKeyFileIsItsOwnError(t *testing.T) {
	directory := t.TempDir()
	_, err := Open(directory)
	if !errors.Is(err, ErrNoKeyFile) {
		t.Fatalf("a missing key file reported %v rather than ErrNoKeyFile", err)
	}

	// And a present but unusable one is a different error, because a truncated file is
	// a different accident from a missing one.
	if err := os.WriteFile(KeyPath(directory), []byte("not a key\n"), 0o600); err != nil {
		t.Fatalf("writing a malformed key file: %v", err)
	}
	if _, err := Open(directory); !errors.Is(err, ErrMalformedKeyFile) {
		t.Errorf("a malformed key file reported %v rather than ErrMalformedKeyFile", err)
	}
}

// TestOpenOrCreateIsIdempotent: a restart reads the key it already had, or the
// credentials sealed under the first one become unreadable on the second start.
func TestOpenOrCreateIsIdempotent(t *testing.T) {
	directory := t.TempDir()
	first, err := OpenOrCreate(directory)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	second, err := OpenOrCreate(directory)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if first.KeyID() != second.KeyID() {
		t.Fatal("a restart produced a different key, so everything sealed before it is lost")
	}

	sealed, err := first.Seal([]byte("value"))
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	opened, err := second.Unseal(sealed)
	if err != nil {
		t.Fatalf("the second start could not open what the first sealed: %v", err)
	}
	if string(opened) != "value" {
		t.Errorf("the round trip returned %q", opened)
	}
}

// TestAuthenticatedEncryptionRoundTripsAndFailsLoudly is AC13.
func TestAuthenticatedEncryptionRoundTripsAndFailsLoudly(t *testing.T) {
	directory := t.TempDir()
	box, err := Create(directory)
	if err != nil {
		t.Fatalf("creating the key file: %v", err)
	}

	plaintext := randomBytes(t, 48)
	sealed, err := box.Seal(plaintext)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}

	t.Run("the three fields are beside the ciphertext", func(t *testing.T) {
		if sealed.Algorithm != AlgorithmAESGCM {
			t.Errorf("the algorithm label is %q", sealed.Algorithm)
		}
		if sealed.KeyID != box.KeyID() {
			t.Errorf("the key identifier is %q rather than this key's", sealed.KeyID)
		}
		if len(sealed.Nonce) == 0 {
			t.Error("there is no nonce")
		}
	})

	t.Run("the stored bytes do not contain the plaintext", func(t *testing.T) {
		if bytes.Contains(sealed.Bytes, plaintext) {
			t.Error("the ciphertext contains the plaintext")
		}
		// And the nonce is not the plaintext either, which a sufficiently careless
		// implementation could manage.
		if bytes.Contains(sealed.Nonce, plaintext) {
			t.Error("the nonce contains the plaintext")
		}
	})

	t.Run("a round trip returns exactly what was stored", func(t *testing.T) {
		opened, err := box.Unseal(sealed)
		if err != nil {
			t.Fatalf("opening: %v", err)
		}
		if !bytes.Equal(opened, plaintext) {
			t.Error("the round trip returned something else")
		}
	})

	t.Run("a tampered ciphertext fails rather than yielding anything", func(t *testing.T) {
		for name, tampered := range map[string]Ciphertext{
			"a flipped bit in the ciphertext": withFlippedByte(sealed, false),
			"a flipped bit in the nonce":      withFlippedByte(sealed, true),
			"a truncated ciphertext":          truncated(sealed),
		} {
			opened, err := box.Unseal(tampered)
			if err == nil {
				t.Errorf("%s opened successfully", name)
			}
			if opened != nil {
				t.Errorf("%s returned %d bytes beside its failure", name, len(opened))
			}
		}
	})

	t.Run("a relabelled ciphertext fails", func(t *testing.T) {
		// The key identifier and the algorithm label are authenticated as additional
		// data, so somebody who can write to the database cannot relabel a ciphertext
		// without the authentication failing.
		relabelled := sealed
		relabelled.Algorithm = "something-else"
		if _, err := box.Unseal(relabelled); !errors.Is(err, ErrUnknownAlgorithm) {
			t.Errorf("a relabelled algorithm reported %v", err)
		}
	})

	t.Run("a nonce is never reused", func(t *testing.T) {
		seen := map[string]bool{}
		for range 64 {
			again, err := box.Seal(plaintext)
			if err != nil {
				t.Fatalf("sealing: %v", err)
			}
			key := string(again.Nonce)
			if seen[key] {
				t.Fatal("a nonce was reused, which breaks the mode outright")
			}
			seen[key] = true
		}
	})
}

// TestAReplacedKeyIsReportedAsSuchAndNotAsTampering is what makes AC14's state
// nameable: the identifier beside the ciphertext says the key is wrong, rather than
// the AEAD saying only that something is.
func TestAReplacedKeyIsReportedAsSuchAndNotAsTampering(t *testing.T) {
	first, err := Create(t.TempDir())
	if err != nil {
		t.Fatalf("creating the first key: %v", err)
	}
	second, err := Create(t.TempDir())
	if err != nil {
		t.Fatalf("creating the second key: %v", err)
	}
	if first.KeyID() == second.KeyID() {
		t.Fatal("two generated keys collided, so this proves nothing")
	}

	sealed, err := first.Seal([]byte("value"))
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	_, err = second.Unseal(sealed)
	if !errors.Is(err, ErrWrongKey) {
		t.Errorf("a ciphertext from another key reported %v rather than ErrWrongKey", err)
	}
	if errors.Is(err, ErrAuthenticationFailed) {
		t.Error("a replaced key is reported as tampering, which is a different accusation")
	}
}

// TestMatchesAcceptsTheKeyFileAndNothingElse is what "possession of the key file"
// rests on.
func TestMatchesAcceptsTheKeyFileAndNothingElse(t *testing.T) {
	directory := t.TempDir()
	box, err := Create(directory)
	if err != nil {
		t.Fatalf("creating the key file: %v", err)
	}
	content, err := os.ReadFile(KeyPath(directory))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}

	if !box.Matches(content) {
		t.Error("the key file itself was not recognised")
	}
	// Whitespace is forgiven, because somebody pasting a file adds or loses a newline.
	if !box.Matches([]byte("  " + string(content) + "\n\n")) {
		t.Error("the key file was not recognised when pasted with stray whitespace")
	}

	// A different, perfectly well-formed key file. This is the case a length check, or
	// a check that the field is merely not empty, would let through.
	otherDirectory := t.TempDir()
	if _, err := Create(otherDirectory); err != nil {
		t.Fatalf("creating another key file: %v", err)
	}
	otherContent, err := os.ReadFile(KeyPath(otherDirectory))
	if err != nil {
		t.Fatalf("reading the other key file: %v", err)
	}

	for name, presented := range map[string][]byte{
		"nothing":              {},
		"a short value":        []byte("abc"),
		"a wrong-length key":   []byte("00112233445566778899aabbccddeeff"),
		"another valid key":    otherContent,
		"the key's identifier": []byte(box.KeyID()),
	} {
		if box.Matches(presented) {
			t.Errorf("%s was accepted as the key file", name)
		}
	}
}

// TestTheKeyFileModeIsAsserted names the one thing the Windows host cannot prove, so
// a green run on a host is not read as evidence.
func TestTheKeyFileModeIsAsserted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not expressible on Windows; every project command runs in " +
			"the Debian container, which is where this is proved")
	}
	directory := t.TempDir()
	if _, err := Create(directory); err != nil {
		t.Fatalf("creating the key file: %v", err)
	}
	info, err := os.Stat(KeyPath(directory))
	if err != nil {
		t.Fatalf("reading the key file: %v", err)
	}
	var expected fs.FileMode = 0o600
	if info.Mode().Perm() != expected {
		t.Errorf("the key file is %04o rather than %04o", info.Mode().Perm(), expected)
	}
}

// withFlippedByte returns the ciphertext with one byte changed.
func withFlippedByte(sealed Ciphertext, inNonce bool) Ciphertext {
	tampered := sealed
	if inNonce {
		nonce := make([]byte, len(sealed.Nonce))
		copy(nonce, sealed.Nonce)
		nonce[0] ^= 0x01
		tampered.Nonce = nonce
		return tampered
	}
	body := make([]byte, len(sealed.Bytes))
	copy(body, sealed.Bytes)
	body[0] ^= 0x01
	tampered.Bytes = body
	return tampered
}

// truncated returns the ciphertext with its last byte removed.
func truncated(sealed Ciphertext) Ciphertext {
	tampered := sealed
	tampered.Bytes = sealed.Bytes[:len(sealed.Bytes)-1]
	return tampered
}

// randomBytes generates a value for one test run.
func randomBytes(t *testing.T, count int) []byte {
	t.Helper()
	raw := make([]byte, count)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test value: %v", err)
	}
	return raw
}
