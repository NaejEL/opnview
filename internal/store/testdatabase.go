package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/NaejEL/opnview/internal/store/sqldb"
)

// Databases for the tests (P9 of specs/SPEC-backend-completion-plugins.md).
//
// EVERY TEST GETS A DATABASE OF ITS OWN, cloned from a template built once. The template is the
// schema applied to an empty database, exactly as a first start applies it; a clone is a
// PostgreSQL CREATE DATABASE ... TEMPLATE, which copies it in a fraction of the time an apply
// takes, so a test pays the clone and not the schema. The template's name carries a digest of the
// schema it holds, so a run whose schema is unchanged reuses the template a previous run left on
// the test instance, and a run whose schema changed builds a new one; it is built under a
// temporary name and renamed when complete, so a run interrupted half-way never leaves a template
// another run would trust. Each clone, and its read role, is dropped by its exact name when the
// test ends.
//
// The test instance is the one OPNVIEW_TEST_DATABASE names, a connection to its maintenance
// database: docker-compose.yml supplies it to the everyday and the pre-deployment commands. This
// file is not a test file because the tests of several packages share it; nothing in the product
// calls it.

// TestDatabaseVariable is the environment variable naming the test instance.
const TestDatabaseVariable = "OPNVIEW_TEST_DATABASE"

// testTemplateRevision is raised when what Open writes into a fresh database changes for a reason
// the schema's digest does not see.
const testTemplateRevision = "2"

var (
	templateOnce sync.Once
	templateName string
	templateErr  error
)

// TestDatabase is one test's database.
type TestDatabase struct {
	// DSN opens it.
	DSN   string
	name  string
	admin string
}

// NewTestDatabase creates an empty database with the schema applied, cloned from the template.
func NewTestDatabase(ctx context.Context) (*TestDatabase, error) {
	admin := os.Getenv(TestDatabaseVariable)
	if admin == "" {
		return nil, fmt.Errorf("store: %s is not set; the tests run inside the development containers, "+
			"which set it (docker compose run --rm checks)", TestDatabaseVariable)
	}
	templateOnce.Do(func() { templateName, templateErr = ensureTemplate(ctx, admin) })
	if templateErr != nil {
		return nil, templateErr
	}
	name, err := uniqueName("opnview_test_")
	if err != nil {
		return nil, err
	}
	if err := createDatabase(ctx, admin, name, templateName); err != nil {
		return nil, err
	}
	dsn, err := withDatabase(admin, name)
	if err != nil {
		return nil, err
	}
	return &TestDatabase{DSN: dsn, name: name, admin: admin}, nil
}

// NewEmptyTestDatabase creates a database with nothing in it, not even the schema, for a test of
// what Open does to an empty or an outdated database.
func NewEmptyTestDatabase(ctx context.Context) (*TestDatabase, error) {
	admin := os.Getenv(TestDatabaseVariable)
	if admin == "" {
		return nil, fmt.Errorf("store: %s is not set", TestDatabaseVariable)
	}
	name, err := uniqueName("opnview_test_")
	if err != nil {
		return nil, err
	}
	if err := createDatabase(ctx, admin, name, ""); err != nil {
		return nil, err
	}
	dsn, err := withDatabase(admin, name)
	if err != nil {
		return nil, err
	}
	return &TestDatabase{DSN: dsn, name: name, admin: admin}, nil
}

// Name is the database's name.
func (d *TestDatabase) Name() string { return d.name }

// Drop drops the database and its read role, by their exact names.
func (d *TestDatabase) Drop(ctx context.Context) error {
	db, err := sqldb.OpenPool(d.admin)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{d.name}.Sanitize()+
		" WITH (FORCE)"); err != nil {
		return fmt.Errorf("store: dropping the test database %s: %w", d.name, err)
	}
	if _, err := db.ExecContext(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{sqldb.ReadRoleName(d.name)}.Sanitize()); err != nil {
		return fmt.Errorf("store: dropping the read role of %s: %w", d.name, err)
	}
	return nil
}

// ensureTemplate returns the template holding this schema, building it when the instance has none.
func ensureTemplate(ctx context.Context, admin string) (string, error) {
	schema, err := sqlFiles.ReadFile("schema.sql")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append(schema, []byte(testTemplateRevision)...))
	name := "opnview_template_" + hex.EncodeToString(digest[:])[:16]
	db, err := sqldb.OpenPool(admin)
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = ?)", name).
		Scan(&exists); err != nil {
		return "", fmt.Errorf("store: looking for the test template: %w", err)
	}
	if exists {
		return name, nil
	}
	building, err := uniqueName("opnview_building_")
	if err != nil {
		return "", err
	}
	if err := createDatabase(ctx, admin, building, ""); err != nil {
		return "", err
	}
	dsn, err := withDatabase(admin, building)
	if err != nil {
		return "", err
	}
	store, err := Open(ctx, dsn)
	if err != nil {
		return "", fmt.Errorf("store: building the test template: %w", err)
	}
	if err := store.Close(); err != nil {
		return "", err
	}
	// Another process may have finished the same template meanwhile: the rename then fails, the
	// built copy is dropped, and the other's is used.
	if _, err := db.ExecContext(ctx, "ALTER DATABASE "+pgx.Identifier{building}.Sanitize()+" RENAME TO "+
		pgx.Identifier{name}.Sanitize()); err != nil {
		_, _ = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{building}.Sanitize()+" WITH (FORCE)")
		_, _ = db.ExecContext(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{sqldb.ReadRoleName(building)}.Sanitize())
		var nowExists bool
		if checkErr := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = ?)",
			name).Scan(&nowExists); checkErr == nil && nowExists {
			return name, nil
		}
		return "", fmt.Errorf("store: naming the test template: %w", err)
	}
	return name, nil
}

// createDatabase creates a database, from a template or from template1 (which carries the
// TimescaleDB extension), with byte-order text.
func createDatabase(ctx context.Context, admin, name, template string) error {
	db, err := sqldb.OpenPool(admin)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	statement := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
	if template != "" {
		statement += " TEMPLATE " + pgx.Identifier{template}.Sanitize() + " STRATEGY FILE_COPY"
	} else {
		statement += " ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C' TEMPLATE template0"
	}
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		_, lastErr = db.ExecContext(ctx, statement)
		if lastErr == nil {
			if template == "" {
				return installExtension(ctx, admin, name)
			}
			return nil
		}
		// 55006: the template is being accessed by another session (a concurrent clone).
		if !strings.Contains(lastErr.Error(), "55006") && !strings.Contains(lastErr.Error(), "being accessed") {
			break
		}
	}
	return fmt.Errorf("store: creating the test database %s: %w", name, lastErr)
}

// installExtension creates the TimescaleDB extension in a database made from template0.
func installExtension(ctx context.Context, admin, name string) error {
	dsn, err := withDatabase(admin, name)
	if err != nil {
		return err
	}
	db, err := sqldb.OpenPool(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS timescaledb"); err != nil {
		return fmt.Errorf("store: loading TimescaleDB in %s: %w", name, err)
	}
	return nil
}

// withDatabase is a connection string naming another database on the same instance.
func withDatabase(dsn, database string) (string, error) {
	if _, err := pgx.ParseConfig(dsn); err != nil {
		return "", err
	}
	if strings.Contains(dsn, "://") {
		return "", errors.New("store: give the test instance as key=value pairs, not as a URL")
	}
	return replaceDatabase(dsn, database), nil
}

// replaceDatabase swaps the dbname of a key=value connection string, or adds one.
func replaceDatabase(dsn, database string) string {
	fields := strings.Fields(dsn)
	replaced := false
	for index, field := range fields {
		if strings.HasPrefix(field, "dbname=") {
			fields[index] = "dbname=" + database
			replaced = true
		}
	}
	if !replaced {
		fields = append(fields, "dbname="+database)
	}
	return strings.Join(fields, " ")
}

// uniqueName is a database name no other test uses.
func uniqueName(prefix string) (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}

// NewTestDatabaseFrom creates a database cloned from another test database, which no connection
// may hold: a fixture built once and given to every test that asks for it.
func NewTestDatabaseFrom(ctx context.Context, template *TestDatabase) (*TestDatabase, error) {
	name, err := uniqueName("opnview_test_")
	if err != nil {
		return nil, err
	}
	if err := createDatabase(ctx, template.admin, name, template.name); err != nil {
		return nil, err
	}
	dsn, err := withDatabase(template.admin, name)
	if err != nil {
		return nil, err
	}
	return &TestDatabase{DSN: dsn, name: name, admin: template.admin}, nil
}
