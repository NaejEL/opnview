package config

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/secret"
	"github.com/NaejEL/opnview/internal/store"
)

// The setting keys cycle 4B adds. All four are configuration, and configuration
// is what this table holds, so a second location for them would be a second
// truth.
//
// THE TWO CIPHERTEXTS ARE NOT HERE. `setting.value` is a single TEXT NOT NULL and
// a ciphertext needs a nonce, an algorithm label and a key identifier beside it;
// they live in `encrypted_credential`. The OPNsense API KEY is here because it is
// the Basic-auth username half of the pair, which ROADMAP.md does not list among
// the secrets.
//
// NO VALUE FOR ANY OF THESE APPEARS ANYWHERE IN THIS REPOSITORY. There is no
// default firewall URL, no placeholder host and no example address: every one of
// them is typed into the settings surface by the person installing the product.
const (
	// KeyOPNsenseBaseURL is the firewall's base URL, exactly as typed, with no
	// trailing slash.
	KeyOPNsenseBaseURL = "opnsense_base_url"
	// KeyOPNsenseAPIKey is the key half of the firewall API pair.
	KeyOPNsenseAPIKey = "opnsense_api_key"
	// KeyOPNsenseCertificateFingerprint is the SHA-256 fingerprint of the
	// certificate the firewall is expected to present, or an absent row.
	//
	// IT IS A SETTING AND NOT A CREDENTIAL. A fingerprint is a hash of a certificate
	// the firewall hands to anybody who connects, so it is public by construction
	// and there is nothing to encrypt. Storing it beside the URL is also what lets
	// the settings surface pre-fill it, which a credential may never be.
	KeyOPNsenseCertificateFingerprint = "opnsense_certificate_fingerprint"
	// KeyTheme is the installation's theme. It is ONE ROW FOR THE INSTALLATION and
	// not a per-account preference: the schema has no such concept and one account
	// cannot justify inventing one.
	KeyTheme = "theme"
	// KeyMaxMindAccountID is the MaxMind account ID, which MaxMind requires beside
	// the licence key to download a database.
	//
	// IT IS A SETTING AND NOT A CREDENTIAL, for the reason the fingerprint is: it
	// identifies an account and grants nothing without the licence key, which stays
	// an encrypted credential. It is a positive whole number.
	KeyMaxMindAccountID = "maxmind_account_id"
)

// FirewallCredentials is the live credential holder the OPNsense client reads.
//
// IT IS THE SEAM THAT MAKES A TYPO RECOVERABLE WITHOUT A RESTART. Cycle 4A built
// the client once, at start-up, from an empty value; a credential entered
// afterwards could not reach a running collector. The client now reads this on
// every call, and the settings surface writes to it as soon as it has stored and
// decrypted what was typed, so the next collection pass uses the new value in the
// same process.
type FirewallCredentials struct {
	mutex sync.RWMutex
	value opnsense.Credentials
	// generation counts how many times the value has been replaced. It exists so
	// a test can assert that a settings write actually reached this holder, rather
	// than inferring it from a request that might have been made for another
	// reason.
	generation uint64
}

// NewFirewallCredentials returns an empty holder. Empty is the honest starting
// state of an installation nobody has configured yet: every collector reports
// that it has no firewall to talk to, which is the same code path a wrong URL
// takes.
func NewFirewallCredentials() *FirewallCredentials { return &FirewallCredentials{} }

// Credentials returns the credentials to use now, satisfying
// opnsense.CredentialSource.
func (f *FirewallCredentials) Credentials() opnsense.Credentials {
	f.mutex.RLock()
	defer f.mutex.RUnlock()
	return f.value
}

// Set replaces the credentials. The next call the client builds uses them.
func (f *FirewallCredentials) Set(credentials opnsense.Credentials) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.value = credentials
	f.generation++
}

// Generation is how many times Set has been called.
func (f *FirewallCredentials) Generation() uint64 {
	f.mutex.RLock()
	defer f.mutex.RUnlock()
	return f.generation
}

// CredentialState is what opnview can say about the stored credentials without
// contacting anything.
//
// THE THREE ARE NOT INTERCHANGEABLE, and the reason this is a type rather than a
// boolean is that two of them were going to be reported as the same sentence. A
// key file that has been lost or replaced is not "no firewall is configured": the
// firewall IS configured, and what is broken is opnview's ability to read the
// secret. Reporting the second as the first would send the operator to look for a
// setting that is already there.
type CredentialState string

// The three states, and no fourth.
const (
	// CredentialStateAbsent means nothing is stored. Nobody has configured a
	// firewall yet, which is the state of every fresh installation.
	CredentialStateAbsent CredentialState = "absent"
	// CredentialStateReady means the stored credentials were decrypted and are in
	// use. It says nothing about whether the firewall answers: that is what
	// verification is for.
	CredentialStateReady CredentialState = "ready"
	// CredentialStateUndecryptable means something is stored and opnview cannot
	// read it: the key file is gone, or it is a different key, or the ciphertext
	// has been tampered with. Re-entry is what resolves it.
	CredentialStateUndecryptable CredentialState = "undecryptable"
)

// CredentialReader is what loading needs from storage.
type CredentialReader interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	Credential(ctx context.Context, name string) (store.EncryptedCredential, bool, error)
}

// Unsealer is what loading needs from the key file. It is an interface so that a
// caller with no key file at all can pass nil and get the undecryptable state
// rather than a panic.
type Unsealer interface {
	Unseal(sealed secret.Ciphertext) ([]byte, error)
}

// LoadFirewallCredentials reads the firewall URL, the API key and the decrypted
// API secret, and says which of the three states the installation is in.
//
// A partially configured installation — a URL and no secret, say — is
// CredentialStateAbsent rather than an error: it is somebody halfway through the
// settings form, not a fault. What is a distinct state is a credential that is
// stored and cannot be opened.
func LoadFirewallCredentials(ctx context.Context, reader CredentialReader,
	unsealer Unsealer) (opnsense.Credentials, CredentialState, error) {
	baseURL, _, err := readSetting(ctx, reader, KeyOPNsenseBaseURL)
	if err != nil {
		return opnsense.Credentials{}, CredentialStateAbsent, err
	}
	apiKey, _, err := readSetting(ctx, reader, KeyOPNsenseAPIKey)
	if err != nil {
		return opnsense.Credentials{}, CredentialStateAbsent, err
	}
	fingerprint, _, err := readSetting(ctx, reader, KeyOPNsenseCertificateFingerprint)
	if err != nil {
		return opnsense.Credentials{}, CredentialStateAbsent, err
	}

	sealed, stored, err := reader.Credential(ctx, store.CredentialOPNsenseAPISecret)
	if err != nil {
		return opnsense.Credentials{}, CredentialStateAbsent, err
	}
	if !stored {
		if baseURL == "" && apiKey == "" {
			return opnsense.Credentials{}, CredentialStateAbsent, nil
		}
		// Half-entered. There is nothing to decrypt, so nothing is undecryptable;
		// the credentials are not usable and the state is the same one a fresh
		// installation is in.
		return opnsense.Credentials{}, CredentialStateAbsent, nil
	}

	if unsealer == nil {
		return opnsense.Credentials{}, CredentialStateUndecryptable, nil
	}
	apiSecret, err := unsealer.Unseal(secret.Ciphertext{
		Algorithm: sealed.Algorithm,
		KeyID:     sealed.KeyID,
		Nonce:     sealed.Nonce,
		Bytes:     sealed.Ciphertext,
	})
	if err != nil {
		// A failure to open is a STATE, not a start-up failure: the service has to
		// come up so the operator can re-enter the credential through the very
		// surface that would otherwise be unreachable. The error is not swallowed —
		// it is what the state means — but it does not stop the process.
		if isUnsealFailure(err) {
			return opnsense.Credentials{}, CredentialStateUndecryptable, nil
		}
		return opnsense.Credentials{}, CredentialStateUndecryptable,
			fmt.Errorf("config: opening the stored firewall secret: %w", err)
	}

	return opnsense.Credentials{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		APISecret:   string(apiSecret),
		Fingerprint: fingerprint,
	}, CredentialStateReady, nil
}

// isUnsealFailure reports whether err is one of the four ways a stored
// ciphertext legitimately fails to open. Anything else is a real fault and is
// returned.
func isUnsealFailure(err error) bool {
	return errors.Is(err, secret.ErrAuthenticationFailed) ||
		errors.Is(err, secret.ErrWrongKey) ||
		errors.Is(err, secret.ErrUnknownAlgorithm) ||
		errors.Is(err, secret.ErrNoKeyFile)
}

// readSetting reads one optional setting row.
func readSetting(ctx context.Context, reader CredentialReader, key string) (string, bool, error) {
	value, present, err := reader.Setting(ctx, key)
	if err != nil {
		return "", false, err
	}
	return value, present, nil
}

// MaxMindCredentials are what a MaxMind database download authenticates with: the
// account ID and the licence key, both required.
type MaxMindCredentials struct {
	// AccountID is the setting row, as typed.
	AccountID string
	// LicenceKey is the decrypted licence key.
	LicenceKey string
}

// LoadMaxMindCredentials reads the MaxMind account ID and the decrypted licence key,
// and says which of the three states the licence key is in.
//
// The states mean what they mean for the firewall: no key stored is absent, a key
// that cannot be opened is undecryptable and is NOT reported as absent, and ready
// says only that the key was opened — whether MaxMind accepts it is what a download
// finds out. The account ID is returned whatever the key's state; a caller with a
// ready key and no account ID has a configuration halfway entered.
func LoadMaxMindCredentials(ctx context.Context, reader CredentialReader,
	unsealer Unsealer) (MaxMindCredentials, CredentialState, error) {
	accountID, _, err := readSetting(ctx, reader, KeyMaxMindAccountID)
	if err != nil {
		return MaxMindCredentials{}, CredentialStateAbsent, err
	}
	loaded := MaxMindCredentials{AccountID: accountID}

	sealed, stored, err := reader.Credential(ctx, store.CredentialMaxMindLicenceKey)
	if err != nil {
		return loaded, CredentialStateAbsent, err
	}
	if !stored {
		return loaded, CredentialStateAbsent, nil
	}
	if unsealer == nil {
		return loaded, CredentialStateUndecryptable, nil
	}
	licenceKey, err := unsealer.Unseal(secret.Ciphertext{
		Algorithm: sealed.Algorithm,
		KeyID:     sealed.KeyID,
		Nonce:     sealed.Nonce,
		Bytes:     sealed.Ciphertext,
	})
	if err != nil {
		if isUnsealFailure(err) {
			return loaded, CredentialStateUndecryptable, nil
		}
		return loaded, CredentialStateUndecryptable,
			fmt.Errorf("config: opening the stored MaxMind licence key: %w", err)
	}
	loaded.LicenceKey = string(licenceKey)
	return loaded, CredentialStateReady, nil
}
