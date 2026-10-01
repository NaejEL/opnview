package config

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
)

// fakeCredentialReader stands in for the database.
//
// It is a map rather than a store so that this package's tests stay tests of this
// package. The same paths are exercised against a real database and a real HTTP
// surface from internal/web.
type fakeCredentialReader struct {
	settings    map[string]string
	credentials map[string]store.EncryptedCredential
}

func newFakeCredentialReader() *fakeCredentialReader {
	return &fakeCredentialReader{
		settings:    map[string]string{},
		credentials: map[string]store.EncryptedCredential{},
	}
}

func (f *fakeCredentialReader) Setting(_ context.Context, key string) (string, bool, error) {
	value, present := f.settings[key]
	return value, present, nil
}

func (f *fakeCredentialReader) Credential(_ context.Context, name string) (
	store.EncryptedCredential, bool, error) {
	credential, present := f.credentials[name]
	return credential, present, nil
}

// store records one sealed credential.
func (f *fakeCredentialReader) put(name string, sealed secret.Ciphertext) {
	f.credentials[name] = store.EncryptedCredential{
		Name: name, Algorithm: sealed.Algorithm, KeyID: sealed.KeyID,
		Nonce: sealed.Nonce, Ciphertext: sealed.Bytes, UpdatedAt: 1_700_000_000,
	}
}

// TestTheThreeCredentialStatesAreDistinguishable is AC14 at the source: the two that
// were going to be reported as one sentence are two values here, so nothing downstream
// has to guess.
func TestTheThreeCredentialStatesAreDistinguishable(t *testing.T) {
	ctx := context.Background()
	box, err := secret.Create(t.TempDir())
	if err != nil {
		t.Fatalf("creating a key: %v", err)
	}

	t.Run("nothing stored is absent", func(t *testing.T) {
		_, state, err := LoadFirewallCredentials(ctx, newFakeCredentialReader(), box)
		if err != nil {
			t.Fatalf("loading: %v", err)
		}
		if state != CredentialStateAbsent {
			t.Errorf("an unconfigured installation reports %q", state)
		}
	})

	t.Run("half entered is still absent rather than an error", func(t *testing.T) {
		// A URL and no secret is somebody halfway through the form, not a fault, and
		// the service must come up either way.
		reader := newFakeCredentialReader()
		reader.settings[KeyOPNsenseBaseURL] = "https://firewall.invalid"
		credentials, state, err := LoadFirewallCredentials(ctx, reader, box)
		if err != nil {
			t.Fatalf("loading: %v", err)
		}
		if state != CredentialStateAbsent {
			t.Errorf("a half-entered installation reports %q", state)
		}
		if credentials.BaseURL != "" {
			t.Error("a half-entered installation handed the collectors a usable base URL")
		}
	})

	t.Run("stored and openable is ready", func(t *testing.T) {
		reader := newFakeCredentialReader()
		apiKey, apiSecret := generated(t), generated(t)
		reader.settings[KeyOPNsenseBaseURL] = "https://firewall.invalid"
		reader.settings[KeyOPNsenseAPIKey] = apiKey
		sealed, err := box.Seal([]byte(apiSecret))
		if err != nil {
			t.Fatalf("sealing: %v", err)
		}
		reader.put(store.CredentialOPNsenseAPISecret, sealed)

		credentials, state, err := LoadFirewallCredentials(ctx, reader, box)
		if err != nil {
			t.Fatalf("loading: %v", err)
		}
		if state != CredentialStateReady {
			t.Fatalf("a configured installation reports %q", state)
		}
		if credentials.APIKey != apiKey || credentials.APISecret != apiSecret {
			t.Error("the decrypted credentials are not the ones that were stored")
		}
	})

	t.Run("stored and unopenable is its own state, not an absence", func(t *testing.T) {
		reader := newFakeCredentialReader()
		reader.settings[KeyOPNsenseBaseURL] = "https://firewall.invalid"
		reader.settings[KeyOPNsenseAPIKey] = generated(t)
		sealed, err := box.Seal([]byte(generated(t)))
		if err != nil {
			t.Fatalf("sealing: %v", err)
		}
		reader.put(store.CredentialOPNsenseAPISecret, sealed)

		// The key file is gone. The service must come up and say WHICH thing is wrong.
		credentials, state, err := LoadFirewallCredentials(ctx, reader, nil)
		if err != nil {
			t.Fatalf("a lost key file failed the load rather than being a state: %v", err)
		}
		if state != CredentialStateUndecryptable {
			t.Fatalf("a lost key file reports %q", state)
		}
		if state == CredentialStateAbsent {
			t.Error("a lost key file is reported as nothing being configured")
		}
		if credentials != (opnsense.Credentials{}) {
			t.Error("an unopenable credential yielded something anyway")
		}

		// A DIFFERENT key file is the same state and is likewise not a failure.
		other, err := secret.Create(t.TempDir())
		if err != nil {
			t.Fatalf("creating another key: %v", err)
		}
		_, replaced, err := LoadFirewallCredentials(ctx, reader, other)
		if err != nil {
			t.Fatalf("a replaced key file failed the load: %v", err)
		}
		if replaced != CredentialStateUndecryptable {
			t.Errorf("a replaced key file reports %q", replaced)
		}
	})

	t.Run("a tampered ciphertext is the same state and never yields a value", func(t *testing.T) {
		reader := newFakeCredentialReader()
		reader.settings[KeyOPNsenseBaseURL] = "https://firewall.invalid"
		sealed, err := box.Seal([]byte(generated(t)))
		if err != nil {
			t.Fatalf("sealing: %v", err)
		}
		sealed.Bytes[0] ^= 0x01
		reader.put(store.CredentialOPNsenseAPISecret, sealed)

		credentials, state, err := LoadFirewallCredentials(ctx, reader, box)
		if err != nil {
			t.Fatalf("a tampered ciphertext failed the load rather than being a state: %v", err)
		}
		if state != CredentialStateUndecryptable {
			t.Errorf("a tampered ciphertext reports %q", state)
		}
		if credentials.APISecret != "" {
			t.Error("a tampered ciphertext yielded a secret")
		}
	})
}

// TestTheLiveHolderIsReadByTheClientAndWrittenByTheSettingsSurface is the seam of
// AC16 in this package: the value changes, and what the client reads changes with it.
func TestTheLiveHolderIsReadByTheClientAndWrittenByTheSettingsSurface(t *testing.T) {
	holder := NewFirewallCredentials()
	if holder.Credentials() != (opnsense.Credentials{}) {
		t.Fatal("a fresh holder is not empty, so something ships a default")
	}
	if holder.Generation() != 0 {
		t.Errorf("a fresh holder reports generation %d", holder.Generation())
	}

	first := opnsense.Credentials{
		BaseURL: "https://firewall.invalid", APIKey: generated(t), APISecret: generated(t),
	}
	holder.Set(first)
	if holder.Credentials() != first {
		t.Fatal("the holder did not take the value it was given")
	}
	if holder.Generation() != 1 {
		t.Errorf("one write reported generation %d", holder.Generation())
	}

	second := first
	second.APISecret = generated(t)
	holder.Set(second)
	if holder.Credentials() != second {
		t.Error("the holder did not take the second value")
	}
	if holder.Generation() != 2 {
		t.Errorf("two writes reported generation %d", holder.Generation())
	}

	// It satisfies the interface the client reads, which is what makes the seam a seam
	// rather than a struct nobody plugged in.
	var source opnsense.CredentialSource = holder
	if source.Credentials() != second {
		t.Error("the holder does not answer as a credential source")
	}
}

// TestTheHolderIsSafeUnderConcurrentUse: the collector loops run concurrently and the
// settings surface writes from a request goroutine, so this is the real access
// pattern rather than a theoretical one.
func TestTheHolderIsSafeUnderConcurrentUse(t *testing.T) {
	holder := NewFirewallCredentials()
	var waiting sync.WaitGroup
	for writer := range 8 {
		waiting.Add(1)
		go func(writer int) {
			defer waiting.Done()
			for range 100 {
				holder.Set(opnsense.Credentials{
					BaseURL: "https://firewall.invalid", APIKey: generated(t),
				})
				_ = holder.Credentials()
				_ = holder.Generation()
			}
			_ = writer
		}(writer)
	}
	waiting.Wait()
	if holder.Generation() != 800 {
		t.Errorf("800 writes reported generation %d", holder.Generation())
	}
}

// generated returns a credential for one test run, so nothing credential-shaped is
// written into the repository.
func generated(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test value: %v", err)
	}
	return hex.EncodeToString(raw)
}
