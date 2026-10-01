package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

// TestASecondApplySeedsNoAccountNoSessionAndNoCredential is AC2 for the three tables
// this cycle adds.
//
// THE SCHEMA IS APPLIED ON EVERY START, so a default that reappeared would silently
// undo something the user did — and for these three tables a default that appeared at
// all would be an account nobody created, a session nobody signed in to, or a
// credential nobody typed.
func TestASecondApplySeedsNoAccountNoSessionAndNoCredential(t *testing.T) {
	directory := t.TempDir()
	ctx := context.Background()

	database, err := Open(ctx, directory)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	for _, table := range []string{"account", "session", "encrypted_credential"} {
		if count := countRows(t, database, table); count != 0 {
			t.Errorf("a freshly applied schema seeded %d rows into %s", count, table)
		}
	}

	// Something the user did, which a second apply must not touch.
	login := "operator-" + randomValue(t, 4)
	hash := somePasswordHash(t)
	accountID, err := database.CreateAccount(ctx, login, hash, 1_700_000_000)
	if err != nil {
		t.Fatalf("creating an account: %v", err)
	}
	if err := database.CreateSession(ctx, Session{
		TokenDigest: randomValue(t, 32), AccountID: accountID,
		CSRFToken: randomValue(t, 16),
		CreatedAt: 1_700_000_000, LastSeenAt: 1_700_000_000, ExpiresAt: 1_700_003_600,
	}); err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	if err := database.SetCredential(ctx, EncryptedCredential{
		Name: CredentialOPNsenseAPISecret, Algorithm: "aes-256-gcm", KeyID: "abcd1234",
		Nonce: []byte("nonce-bytes"), Ciphertext: []byte("sealed-bytes"),
		UpdatedAt: 1_700_000_000,
	}); err != nil {
		t.Fatalf("storing a credential: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	// The second apply, which is what every start does.
	reopened, err := Open(ctx, directory)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("closing: %v", err)
		}
	})
	for table, expected := range map[string]int{
		"account": 1, "session": 1, "encrypted_credential": 1,
	} {
		if count := countRows(t, reopened, table); count != expected {
			t.Errorf("%s holds %d rows after a second apply rather than %d",
				table, count, expected)
		}
	}
	account, found, err := reopened.AccountByLogin(ctx, login)
	if err != nil || !found {
		t.Fatalf("the account did not survive the second apply: found=%v err=%v", found, err)
	}
	if !bytes.Equal(account.Password.Digest, hash.Digest) {
		t.Error("the second apply changed the stored password")
	}
}

// TestTheDataDirectoryIsCreated is AC3's first clause: a data directory that does not
// exist yet is created rather than refused, because a first start against a fresh
// installation has nothing there.
func TestTheDataDirectoryIsCreated(t *testing.T) {
	nested := t.TempDir() + "/not/yet/there"
	database, err := Open(context.Background(), nested)
	if err != nil {
		t.Fatalf("opening a database in a directory that does not exist: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Errorf("closing: %v", err)
	}
}

// TestOverwritingACredentialLeavesNoPriorCiphertext is AC16's storage clause.
func TestOverwritingACredentialLeavesNoPriorCiphertext(t *testing.T) {
	ctx := context.Background()
	database := openTemporary(t)

	first := []byte("first-sealed-" + randomValue(t, 8))
	second := []byte("second-sealed-" + randomValue(t, 8))

	for _, sealed := range [][]byte{first, second} {
		if err := database.SetCredential(ctx, EncryptedCredential{
			Name: CredentialOPNsenseAPISecret, Algorithm: "aes-256-gcm",
			KeyID: "abcd1234", Nonce: []byte("nonce"), Ciphertext: sealed,
			UpdatedAt: 1_700_000_000,
		}); err != nil {
			t.Fatalf("storing a credential: %v", err)
		}
	}

	if count := countRows(t, database, "encrypted_credential"); count != 1 {
		t.Fatalf("there are %d rows rather than one, so a prior ciphertext survives", count)
	}
	stored, found, err := database.Credential(ctx, CredentialOPNsenseAPISecret)
	if err != nil || !found {
		t.Fatalf("reading the credential: found=%v err=%v", found, err)
	}
	if !bytes.Equal(stored.Ciphertext, second) {
		t.Error("the stored ciphertext is not the one written last")
	}
	if bytes.Contains(stored.Ciphertext, first) {
		t.Error("the current row still carries the previous ciphertext")
	}

	// AND THE PRAGMA THE CLAIM RESTS ON IS ACTUALLY IN FORCE. secure_delete is FAST,
	// which SQLite reports as 2: it zeroes what is freed inside a page being
	// rewritten, which is what a credential row replaced in place goes through. A typo
	// in the DSN would leave it off altogether and nothing else in this package would
	// notice.
	var secureDelete int
	if err := database.DB().QueryRowContext(ctx, "PRAGMA secure_delete").Scan(
		&secureDelete); err != nil {
		t.Fatalf("reading secure_delete: %v", err)
	}
	if secureDelete != 2 {
		t.Errorf("secure_delete is %d rather than 2, which is FAST", secureDelete)
	}
}

// TestAPasswordChangeRevokesEverySessionOfThatAccount: a password change whose old
// sessions survive has changed nothing for whoever was holding one.
func TestAPasswordChangeRevokesEverySessionOfThatAccount(t *testing.T) {
	ctx := context.Background()
	database := openTemporary(t)

	accountID, err := database.CreateAccount(ctx, "operator", somePasswordHash(t), 1_700_000_000)
	if err != nil {
		t.Fatalf("creating an account: %v", err)
	}
	digests := []string{randomValue(t, 32), randomValue(t, 32), randomValue(t, 32)}
	for _, digest := range digests {
		if err := database.CreateSession(ctx, Session{
			TokenDigest: digest, AccountID: accountID, CSRFToken: randomValue(t, 16),
			CreatedAt: 1_700_000_000, LastSeenAt: 1_700_000_000, ExpiresAt: 1_700_003_600,
		}); err != nil {
			t.Fatalf("creating a session: %v", err)
		}
	}
	if count := countRows(t, database, "session"); count != len(digests) {
		t.Fatalf("%d sessions exist rather than %d", count, len(digests))
	}

	if err := database.SetAccountPassword(ctx, accountID, somePasswordHash(t),
		1_700_000_100); err != nil {
		t.Fatalf("changing the password: %v", err)
	}
	if count := countRows(t, database, "session"); count != 0 {
		t.Errorf("%d sessions survived a password change", count)
	}
}

// TestDeletingAnAccountCascadesToItsSessions: the foreign key is what makes a session
// unable to outlive the account it belongs to.
func TestDeletingAnAccountCascadesToItsSessions(t *testing.T) {
	ctx := context.Background()
	database := openTemporary(t)

	accountID, err := database.CreateAccount(ctx, "operator", somePasswordHash(t), 1_700_000_000)
	if err != nil {
		t.Fatalf("creating an account: %v", err)
	}
	if err := database.CreateSession(ctx, Session{
		TokenDigest: randomValue(t, 32), AccountID: accountID, CSRFToken: randomValue(t, 16),
		CreatedAt: 1_700_000_000, LastSeenAt: 1_700_000_000, ExpiresAt: 1_700_003_600,
	}); err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	if _, err := database.DB().ExecContext(ctx, "DELETE FROM account WHERE id = ?",
		accountID); err != nil {
		t.Fatalf("deleting the account: %v", err)
	}
	if count := countRows(t, database, "session"); count != 0 {
		t.Errorf("%d sessions outlived their account", count)
	}
}

// TestASessionCannotNameAnAccountThatDoesNotExist.
func TestASessionCannotNameAnAccountThatDoesNotExist(t *testing.T) {
	database := openTemporary(t)
	err := database.CreateSession(context.Background(), Session{
		TokenDigest: randomValue(t, 32), AccountID: 999_999_999,
		CSRFToken: randomValue(t, 16),
		CreatedAt: 1_700_000_000, LastSeenAt: 1_700_000_000, ExpiresAt: 1_700_003_600,
	})
	if err == nil {
		t.Error("a session naming no account was accepted")
	}
}

// TestASecondAccountWithTheSameLoginIsRefused: the UNIQUE login is the backstop
// beneath the guard CreateAccount carries.
//
// IT IS ASSERTED AGAINST THE DDL DIRECTLY, by an insert of its own, because
// CreateAccount now refuses a second account whatever its login and so can no longer
// reach the constraint. The constraint still matters: it is what a second write path
// added later would meet.
func TestASecondAccountWithTheSameLoginIsRefused(t *testing.T) {
	ctx := context.Background()
	database := openTemporary(t)
	hash := somePasswordHash(t)
	if _, err := database.CreateAccount(ctx, "operator", hash, 1_700_000_000); err != nil {
		t.Fatalf("creating an account: %v", err)
	}
	if _, err := database.DB().ExecContext(ctx,
		`INSERT INTO account (login, password_algorithm, password_memory_kib,
		     password_iterations, password_parallelism, password_salt,
		     password_digest, created_at, password_set_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"operator", hash.Algorithm, hash.MemoryKiB, hash.Iterations, hash.Parallelism,
		hash.Salt, hash.Digest, 1_700_000_000, 1_700_000_000); err == nil {
		t.Error("a second account with the same login was accepted")
	}
}

// TestCreateAccountRefusesASecondAccountWhateverItsLogin is the time-of-check /
// time-of-use window the setup surface used to leave open.
//
// THE COUNT AND THE INSERT WERE TWO STATEMENTS. The middleware counted the accounts
// and the handler inserted afterwards, so two requests carrying the same valid setup
// token and two DIFFERENT logins both passed the count and both inserted — the UNIQUE
// login being a backstop only when the logins collide. The guard is inside
// CreateAccount's own statement now, and this drives the two steps in the order that
// exposed the window: a caller that has just seen an empty table inserts anyway.
func TestCreateAccountRefusesASecondAccountWhateverItsLogin(t *testing.T) {
	ctx := context.Background()
	database := openTemporary(t)

	count, err := database.CountAccounts(ctx)
	if err != nil {
		t.Fatalf("counting accounts: %v", err)
	}
	if count != 0 {
		t.Fatalf("a fresh database holds %d accounts", count)
	}
	if _, err := database.CreateAccount(ctx, "first-"+randomValue(t, 4),
		somePasswordHash(t), 1_700_000_000); err != nil {
		t.Fatalf("creating the account: %v", err)
	}

	// The second caller is the one that read the count above: it saw an empty table and
	// carries a login of its own, which is the case no UNIQUE constraint catches.
	if _, err := database.CreateAccount(ctx, "second-"+randomValue(t, 4),
		somePasswordHash(t), 1_700_000_100); !errors.Is(err, ErrAccountExists) {
		t.Errorf("a second account with a different login was accepted: err=%v", err)
	}
	if count := countRows(t, database, "account"); count != 1 {
		t.Errorf("the installation holds %d accounts rather than one", count)
	}
}

// TestDeletingASettingLeavesNoRow: an empty value must be an absent row rather than a
// row holding the empty string, or "not configured" is two states.
func TestDeletingASettingLeavesNoRow(t *testing.T) {
	ctx := context.Background()
	database := openTemporary(t)

	if err := database.SetSetting(ctx, "a_key", "a_value", 1_700_000_000); err != nil {
		t.Fatalf("writing a setting: %v", err)
	}
	if _, present, err := database.Setting(ctx, "a_key"); err != nil || !present {
		t.Fatalf("the setting was not written: present=%v err=%v", present, err)
	}
	if err := database.DeleteSetting(ctx, "a_key"); err != nil {
		t.Fatalf("deleting a setting: %v", err)
	}
	value, present, err := database.Setting(ctx, "a_key")
	if err != nil {
		t.Fatalf("reading a deleted setting: %v", err)
	}
	if present {
		t.Errorf("the row survived deletion, holding %q", value)
	}
}

// countRows counts one table's rows.
func countRows(t *testing.T, database *Store, table string) int {
	t.Helper()
	var count int
	if err := database.DB().QueryRow(`SELECT count(*) FROM "` + table + `"`).Scan(&count); err != nil {
		t.Fatalf("counting the rows of %s: %v", table, err)
	}
	return count
}

// openTemporary opens a database in a fresh directory and closes it at the end.
func openTemporary(t *testing.T) *Store {
	t.Helper()
	database, err := Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("closing the database: %v", err)
		}
	})
	return database
}

// somePasswordHash is a record of the right shape. It is not a real Argon2id output —
// deriving one here would import internal/auth into its own dependency — and what these
// tests assert is storage rather than hashing.
func somePasswordHash(t *testing.T) PasswordHash {
	t.Helper()
	return PasswordHash{
		Algorithm: "argon2id", MemoryKiB: 65536, Iterations: 3, Parallelism: 4,
		Salt:   []byte(randomValue(t, 8)),
		Digest: []byte(randomValue(t, 16)),
	}
}

// randomValue generates a hex value of the given byte length, so nothing in the
// repository is a fixed credential-shaped string.
func randomValue(t *testing.T, bytes int) string {
	t.Helper()
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a test value: %v", err)
	}
	const digits = "0123456789abcdef"
	encoded := make([]byte, 0, len(raw)*2)
	for _, value := range raw {
		encoded = append(encoded, digits[value>>4], digits[value&0x0f])
	}
	return string(encoded)
}
