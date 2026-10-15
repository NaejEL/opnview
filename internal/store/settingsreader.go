package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/store/accounts"
	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// SettingsReader reads the settings and the sealed credentials of a database a running opnview may
// hold: it takes no instance lock, applies no schema and reads in read-only transactions. It is how a
// one-off command run beside the service -- `opnview measure-cost`, the LV-C measuring tool --
// reads the firewall's address and credentials without stopping the service or writing a byte.
type SettingsReader struct {
	db *sql.DB
}

// OpenSettingsReader connects to a database for reading its settings.
func OpenSettingsReader(ctx context.Context, dsn string) (*SettingsReader, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, ErrNoDatabase
	}
	db, err := sqldb.OpenPool(dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: reaching the database: %w", err)
	}
	return &SettingsReader{db: db}, nil
}

// Close closes the connection.
func (r *SettingsReader) Close() error { return r.db.Close() }

// readOnly runs one read in a read-only transaction.
func (r *SettingsReader) readOnly(ctx context.Context, read func(*sql.Tx) error) error {
	transaction, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	return read(transaction)
}

// Setting reads one setting row.
func (r *SettingsReader) Setting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.readOnly(ctx, func(transaction *sql.Tx) error {
		return transaction.QueryRowContext(ctx, "SELECT value FROM setting WHERE key = ?", key).Scan(&value)
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("store: reading setting %s: %w", key, err)
	}
	return value, true, nil
}

// Credential reads one sealed credential.
func (r *SettingsReader) Credential(ctx context.Context, name string) (accounts.EncryptedCredential, bool, error) {
	var credential accounts.EncryptedCredential
	err := r.readOnly(ctx, func(transaction *sql.Tx) error {
		return transaction.QueryRowContext(ctx,
			`SELECT name, algorithm, key_id, nonce, ciphertext, updated_at
			 FROM encrypted_credential WHERE name = ?`, name).Scan(
			&credential.Name, &credential.Algorithm, &credential.KeyID,
			&credential.Nonce, &credential.Ciphertext, &credential.UpdatedAt)
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return accounts.EncryptedCredential{}, false, nil
	case err != nil:
		return accounts.EncryptedCredential{}, false, fmt.Errorf("store: reading the credential %s: %w", name, err)
	}
	return credential, true, nil
}
