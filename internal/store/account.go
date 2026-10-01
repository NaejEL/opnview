package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The typed read and write paths for the three tables cycle 4B adds: the
// account, the session and the encrypted credential.
//
// NOTHING HERE RETURNS A PASSWORD, A SALT OR A SESSION TOKEN TO A CALLER THAT
// DID NOT ALREADY HAVE IT. An Account carries the digest and the parameters
// because verification needs them and verification happens in internal/auth; no
// HTTP handler reads those fields, and a test asserts no response body carries
// them. The session token is never stored at all — what is stored is a digest of
// it, so this package cannot hand back a token even if something asked.

// The names of the two encrypted credentials. They are the exact two
// ROADMAP.md, "Rules that apply to every step", names as secrets: the OPNsense
// secret and the MaxMind key. The OPNsense API key is the Basic-auth username
// half of the pair and is a setting row, not a ciphertext.
const (
	// CredentialOPNsenseAPISecret is the secret half of the firewall API pair.
	CredentialOPNsenseAPISecret = "opnsense_api_secret"
	// CredentialMaxMindLicenceKey is the MaxMind licence key. It is stored in
	// this cycle and not verified: verifying it means downloading, and the
	// download is cycle 4C.
	CredentialMaxMindLicenceKey = "maxmind_licence_key"
)

// PasswordHash is a stored password verifier: the digest, the salt it was
// derived with, and the parameters it was derived under.
//
// THE PARAMETERS TRAVEL WITH THE RECORD, which is the whole of what ROADMAP.md
// means by storing them beside the hash so they can be raised later. Raising the
// product's defaults changes what a new record is written with; a record already
// written still verifies, because verification reads these fields rather than
// the defaults.
type PasswordHash struct {
	// Algorithm is the label of the scheme that produced Digest.
	Algorithm string
	// MemoryKiB, Iterations and Parallelism are that scheme's cost parameters.
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	// Salt is this account's own salt.
	Salt []byte
	// Digest is the derived key. It is never compared other than in constant
	// time, and never returned in a response.
	Digest []byte
}

// Account is one opnview login.
type Account struct {
	// ID is the primary key.
	ID int64
	// Login is the name typed at sign-in.
	Login string
	// Password is the stored verifier.
	Password PasswordHash
	// CreatedAt is when the account was created, as a UTC epoch in seconds.
	CreatedAt int64
	// PasswordSetAt is when the password was last written.
	PasswordSetAt int64
}

// CountAccounts returns how many accounts exist.
//
// IT IS THE STATE THAT CLOSES THE SETUP SURFACE, and it is a query rather than a
// flag deliberately. A flag in memory is reset by a restart, and a restart that
// reopens setup is the classic first-run hole: anybody on the network wins the
// race. This is derived from the database on every request, on every method.
func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM account").Scan(&count); err != nil {
		return 0, fmt.Errorf("store: counting accounts: %w", err)
	}
	return count, nil
}

// ErrAccountExists is what CreateAccount returns when the installation already
// has an account. It is its own error because the setup surface answers it with
// the permanent closure the specification asks for, and not as an internal
// failure.
var ErrAccountExists = errors.New("store: the installation already has an account")

// CreateAccount inserts the account of an installation that has none, and
// returns its id. It returns ErrAccountExists when one is already there.
//
// THE COUNT AND THE INSERT ARE ONE STATEMENT, and that is what makes "this cycle
// creates exactly one account" true rather than nearly true. A caller that counts
// first and inserts afterwards leaves a window between the two: two requests
// carrying the same valid setup token and two DIFFERENT logins both pass the
// count and both insert, and the UNIQUE login is a backstop only when the logins
// collide. The guard being inside the statement means SQLite's own write
// serialisation settles the race, and the loser is told the installation has an
// account rather than quietly getting a second one.
func (s *Store) CreateAccount(ctx context.Context, login string, password PasswordHash,
	now int64) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO account (login, password_algorithm, password_memory_kib,
		     password_iterations, password_parallelism, password_salt,
		     password_digest, created_at, password_set_at)
		 SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM account)`,
		login, password.Algorithm, password.MemoryKiB, password.Iterations,
		password.Parallelism, password.Salt, password.Digest, now, now)
	if err != nil {
		return 0, fmt.Errorf("store: creating an account: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: reading whether the account was created: %w", err)
	}
	if inserted == 0 {
		return 0, ErrAccountExists
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: reading the new account id: %w", err)
	}
	return id, nil
}

// AccountByLogin returns one account by its login, and whether it exists.
//
// A caller must not branch on the boolean in a way a client can observe: the two
// sign-in failures — no such login, and the wrong password — are required to be
// indistinguishable in what they disclose. internal/web does the verification
// work in both cases for that reason.
func (s *Store) AccountByLogin(ctx context.Context, login string) (Account, bool, error) {
	return s.scanAccount(ctx,
		`SELECT id, login, password_algorithm, password_memory_kib, password_iterations,
		        password_parallelism, password_salt, password_digest, created_at, password_set_at
		 FROM account WHERE login = ?`, login)
}

// AnyAccount returns the single account of an installation that has one. The
// recovery path uses it: a reset authorised by the key file has no login to go
// on beyond the one the user types, and it must be able to say that the login
// typed is not the one that exists.
func (s *Store) AnyAccount(ctx context.Context) (Account, bool, error) {
	return s.scanAccount(ctx,
		`SELECT id, login, password_algorithm, password_memory_kib, password_iterations,
		        password_parallelism, password_salt, password_digest, created_at, password_set_at
		 FROM account ORDER BY id LIMIT 1`)
}

// scanAccount runs one account query and decodes its single row.
func (s *Store) scanAccount(ctx context.Context, query string, arguments ...any) (Account, bool, error) {
	var account Account
	err := s.db.QueryRowContext(ctx, query, arguments...).Scan(
		&account.ID, &account.Login, &account.Password.Algorithm,
		&account.Password.MemoryKiB, &account.Password.Iterations,
		&account.Password.Parallelism, &account.Password.Salt,
		&account.Password.Digest, &account.CreatedAt, &account.PasswordSetAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Account{}, false, nil
	case err != nil:
		return Account{}, false, fmt.Errorf("store: reading an account: %w", err)
	}
	return account, true, nil
}

// SetAccountPassword replaces one account's password verifier, parameters
// included, and destroys every session it had.
//
// THE SESSIONS GO WITH IT. A password change whose old sessions survive has
// changed nothing for whoever was holding one.
func (s *Store) SetAccountPassword(ctx context.Context, accountID int64,
	password PasswordHash, now int64) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: starting a password change: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	if _, err := transaction.ExecContext(ctx,
		`UPDATE account
		 SET password_algorithm = ?, password_memory_kib = ?, password_iterations = ?,
		     password_parallelism = ?, password_salt = ?, password_digest = ?,
		     password_set_at = ?
		 WHERE id = ?`,
		password.Algorithm, password.MemoryKiB, password.Iterations,
		password.Parallelism, password.Salt, password.Digest, now, accountID); err != nil {
		return fmt.Errorf("store: writing a password: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		"DELETE FROM session WHERE account_id = ?", accountID); err != nil {
		return fmt.Errorf("store: revoking the sessions of an account: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: committing a password change: %w", err)
	}
	return nil
}

// Session is one signed-in browser, as stored.
type Session struct {
	// TokenDigest is the primary key: a digest of the token, never the token.
	TokenDigest string
	// AccountID is the account the session belongs to.
	AccountID int64
	// CSRFToken is this session's own form token.
	CSRFToken string
	// CreatedAt, LastSeenAt and ExpiresAt are UTC epochs in seconds.
	// ExpiresAt is the absolute cap and never moves; the sliding idle window is
	// LastSeenAt plus a duration internal/auth carries, and is not a column.
	CreatedAt  int64
	LastSeenAt int64
	ExpiresAt  int64
}

// CreateSession inserts one session row.
func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session (token_digest, account_id, csrf_token, created_at,
		     last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		session.TokenDigest, session.AccountID, session.CSRFToken,
		session.CreatedAt, session.LastSeenAt, session.ExpiresAt)
	if err != nil {
		return fmt.Errorf("store: creating a session: %w", err)
	}
	return nil
}

// SessionByTokenDigest returns one session, and whether it exists.
func (s *Store) SessionByTokenDigest(ctx context.Context, digest string) (Session, bool, error) {
	var session Session
	err := s.db.QueryRowContext(ctx,
		`SELECT token_digest, account_id, csrf_token, created_at, last_seen_at, expires_at
		 FROM session WHERE token_digest = ?`, digest).Scan(
		&session.TokenDigest, &session.AccountID, &session.CSRFToken,
		&session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Session{}, false, nil
	case err != nil:
		return Session{}, false, fmt.Errorf("store: reading a session: %w", err)
	}
	return session, true, nil
}

// TouchSession moves the sliding idle window forward. It never moves
// expires_at: the absolute cap is written once and the whole point of it is that
// activity cannot extend it.
func (s *Store) TouchSession(ctx context.Context, digest string, now int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE session SET last_seen_at = ? WHERE token_digest = ?", now, digest)
	if err != nil {
		return fmt.Errorf("store: touching a session: %w", err)
	}
	return nil
}

// DeleteSession removes one session. Signing out is this, and a replay of the
// token afterwards finds no row.
func (s *Store) DeleteSession(ctx context.Context, digest string) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM session WHERE token_digest = ?", digest); err != nil {
		return fmt.Errorf("store: deleting a session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes every session past its absolute cap, and every
// session whose idle window closed before idleCutoff. It is what keeps the table
// bounded; a session past either bound is refused whether or not this has run.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now, idleCutoff int64) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM session WHERE expires_at <= ? OR last_seen_at <= ?",
		now, idleCutoff); err != nil {
		return fmt.Errorf("store: collecting expired sessions: %w", err)
	}
	return nil
}

// EncryptedCredential is one credential at rest: the ciphertext, and the three
// things without which it cannot be opened or a replaced key told apart from a
// tampered ciphertext.
type EncryptedCredential struct {
	// Name says which credential this is.
	Name string
	// Algorithm is the label of the scheme that sealed it.
	Algorithm string
	// KeyID identifies the key that sealed it.
	KeyID string
	// Nonce is the nonce it was sealed with.
	Nonce []byte
	// Ciphertext is the sealed bytes, authentication tag included.
	Ciphertext []byte
	// UpdatedAt is when it was written, as a UTC epoch in seconds.
	UpdatedAt int64
}

// SetCredential writes one credential, replacing whatever was there.
//
// IT REPLACES THE ROW RATHER THAN ADDING ONE, so no prior ciphertext survives
// beside the current one. There is no history of a credential: a previous
// ciphertext left readable in the database is a previous secret left readable to
// anyone who also has the key file.
func (s *Store) SetCredential(ctx context.Context, credential EncryptedCredential) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO encrypted_credential (name, algorithm, key_id, nonce, ciphertext, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (name) DO UPDATE SET
		     algorithm = excluded.algorithm, key_id = excluded.key_id,
		     nonce = excluded.nonce, ciphertext = excluded.ciphertext,
		     updated_at = excluded.updated_at`,
		credential.Name, credential.Algorithm, credential.KeyID,
		credential.Nonce, credential.Ciphertext, credential.UpdatedAt)
	if err != nil {
		return fmt.Errorf("store: writing the credential %s: %w", credential.Name, err)
	}
	return nil
}

// Credential returns one credential, and whether it exists.
func (s *Store) Credential(ctx context.Context, name string) (EncryptedCredential, bool, error) {
	var credential EncryptedCredential
	err := s.db.QueryRowContext(ctx,
		`SELECT name, algorithm, key_id, nonce, ciphertext, updated_at
		 FROM encrypted_credential WHERE name = ?`, name).Scan(
		&credential.Name, &credential.Algorithm, &credential.KeyID,
		&credential.Nonce, &credential.Ciphertext, &credential.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return EncryptedCredential{}, false, nil
	case err != nil:
		return EncryptedCredential{}, false, fmt.Errorf("store: reading the credential %s: %w", name, err)
	}
	return credential, true, nil
}

// DeleteSetting removes one setting row: an empty value would be a second way of
// saying "not configured", and the interface reports one state.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM setting WHERE key = ?", key); err != nil {
		return fmt.Errorf("store: deleting setting %s: %w", key, err)
	}
	return nil
}
