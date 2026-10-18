package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

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
// another run would trust. Each clone is dropped by its exact name when the test ends; its read
// role is the template's, shared by every clone and kept with the template (sharedReadRoles below).
//
// The test instance is the one OPNVIEW_TEST_DATABASE names, a connection to its maintenance
// database: docker-compose.yml supplies it to the everyday and the pre-deployment commands. This
// file is not a test file because the tests of several packages share it; nothing in the product
// calls it.

// TestDatabaseVariable is the environment variable naming the test instance.
const TestDatabaseVariable = "OPNVIEW_TEST_DATABASE"

// CloneStrategyVariable names the CREATE DATABASE strategy a test database is cloned from the
// template with: FILE_COPY, the default, or WAL_LOG. docker-compose.yml sets it per instance, from
// measured figures: the PostgreSQL 17 documentation of CREATE DATABASE says FILE_COPY "will force the
// system to perform a checkpoint both before and after the creation of the new database"
// (https://www.postgresql.org/docs/17/sql-createdatabase.html). On the everyday instance, whose
// data is on a tmpfs with durability off, a checkpoint costs next to nothing and FILE_COPY copies a
// small template fastest (11 ms a clone against 18 ms for WAL_LOG, 64 clones 16 at a time, measured
// on 16 October 2026); on the pre-deployment instance, a disk with production durability, each of
// those checkpoints writes and syncs every dirty page of the instance, and 64 create-then-drop
// cycles 16 at a time took 38.3 s with FILE_COPY and 2.6 s with WAL_LOG.
const CloneStrategyVariable = "OPNVIEW_TEST_CLONE_STRATEGY"

// cloneStrategy is the strategy CloneStrategyVariable names; an unset variable is FILE_COPY, and
// any value but the two is refused rather than read as one of them.
func cloneStrategy() (string, error) {
	switch strategy := os.Getenv(CloneStrategyVariable); strategy {
	case "", "FILE_COPY":
		return "FILE_COPY", nil
	case "WAL_LOG":
		return strategy, nil
	default:
		return "", fmt.Errorf("store: %s is %q, neither FILE_COPY nor WAL_LOG", CloneStrategyVariable, strategy)
	}
}

// testTemplateRevision is raised when what Open writes into a fresh database changes for a reason
// the schema's digest does not see.
const testTemplateRevision = "4"

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
	// pkg is the package of the test it was created for, and created the instant it was, for the
	// suite's breakdown (internal/testtiming): its drop runs where no test is on the stack.
	pkg     string
	created time.Time
}

// newTestDatabase is a created database, its clone recorded for the suite's breakdown.
func newTestDatabase(dsn, name, admin string, cloneStarted time.Time) *TestDatabase {
	database := &TestDatabase{DSN: dsn, name: name, admin: admin, pkg: timingPackage(), created: time.Now()}
	recordTimingFor(database.pkg, TimingClone, database.created.Sub(cloneStarted))
	return database
}

// TestDatabaseOptions says what a test database is cloned from. The zero value clones the base template:
// an empty database with the schema applied and the built-ins' registry rows written.
type TestDatabaseOptions struct {
	// Prepare, when set, clones the derived template of a preparation instead: the base template with
	// Prepare run once over it. Revision must change whenever what Prepare writes does. Prepare is handed
	// the connection string of the database it prepares, and closes every connection it opens before it
	// returns, since a database being cloned holds none.
	Revision string
	Prepare  func(ctx context.Context, dsn string) error
	// From, when set, clones another test database, which no connection may hold: a fixture built once
	// and given to every test that asks for it.
	From *TestDatabase
}

// NewTestDatabase creates a test database, cloned as its options say.
func NewTestDatabase(ctx context.Context, options TestDatabaseOptions) (*TestDatabase, error) {
	if options.From != nil {
		role, shared := sharedReadRole(options.From.name)
		if !shared {
			role = sqldb.ReadRoleName(options.From.name)
		}
		return cloneTestDatabase(ctx, options.From.admin, options.From.name, role)
	}
	admin := os.Getenv(TestDatabaseVariable)
	if admin == "" {
		return nil, fmt.Errorf("store: %s is not set; the tests run inside the development containers, "+
			"which set it (docker compose run --rm checks)", TestDatabaseVariable)
	}
	templateOnce.Do(func() { templateName, templateErr = ensureTemplate(ctx, admin) })
	if templateErr != nil {
		return nil, templateErr
	}
	template := templateName
	if options.Prepare != nil {
		derivedTemplates.mutex.Lock()
		derived, found := derivedTemplates.built[options.Revision]
		if !found {
			derived = &derivedTemplate{}
			derivedTemplates.built[options.Revision] = derived
		}
		derivedTemplates.mutex.Unlock()
		derived.once.Do(func() {
			derived.name, derived.err = ensureDerivedTemplate(ctx, admin, options.Revision, options.Prepare)
		})
		if derived.err != nil {
			return nil, derived.err
		}
		template = derived.name
	}
	return cloneTestDatabase(ctx, admin, template, sqldb.ReadRoleName(templateName))
}

// cloneTestDatabase creates a database cloned from a template, its read connections logging in as a
// shared role.
func cloneTestDatabase(ctx context.Context, admin, template, role string) (*TestDatabase, error) {
	name, err := uniqueName("opnview_test_")
	if err != nil {
		return nil, err
	}
	started := time.Now()
	if err := createDatabase(ctx, admin, name, template); err != nil {
		return nil, err
	}
	shareReadRole(name, role)
	dsn, err := withDatabase(admin, name)
	if err != nil {
		return nil, err
	}
	return newTestDatabase(dsn, name, admin, started), nil
}

// A DERIVED TEMPLATE is the template with something more written into it once: a preparation a test
// world would otherwise repeat in every database it creates, such as loading the built-in plugins --
// their tables, their registry rows, their measures -- which cost a plugin test world about 450 ms of
// database time under the suite's load (carried item 1 of Lot 4 of the plugin-API amendment). Its name
// carries the base template's name and the preparation's revision, which the caller derives from
// everything the preparation reads, so a changed plugin builds a new one, and an unchanged one is
// reused across runs as the base template is. It is built under a temporary name and renamed when
// complete, as the base is, and its clones share the base template's read role.
var derivedTemplates = struct {
	mutex sync.Mutex
	built map[string]*derivedTemplate
}{built: map[string]*derivedTemplate{}}

// derivedTemplate is one derived template of this process: built once, then cloned.
type derivedTemplate struct {
	once sync.Once
	name string
	err  error
}

// ensureDerivedTemplate returns the derived template of a preparation, building it from the base
// template when the instance has none.
func ensureDerivedTemplate(ctx context.Context, admin, revision string,
	prepare func(ctx context.Context, dsn string) error) (string, error) {
	digest := sha256.Sum256([]byte(templateName + "\x00" + revision))
	name := "opnview_template_" + hex.EncodeToString(digest[:])[:16]
	db, err := adminPool(admin)
	if err != nil {
		return "", err
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).
		Scan(&exists); err != nil {
		return "", fmt.Errorf("store: looking for a derived test template: %w", err)
	}
	if exists {
		return name, nil
	}
	building, err := uniqueName("opnview_building_")
	if err != nil {
		return "", err
	}
	if err := createDatabase(ctx, admin, building, templateName); err != nil {
		return "", err
	}
	shareReadRole(building, sqldb.ReadRoleName(templateName))
	defer forgetSharedReadRole(building)
	dsn, err := withDatabase(admin, building)
	if err != nil {
		return "", err
	}
	if err := prepare(ctx, dsn); err != nil {
		_, _ = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{building}.Sanitize()+" WITH (FORCE)")
		return "", fmt.Errorf("store: preparing a derived test template: %w", err)
	}
	if _, err := db.ExecContext(ctx, "ALTER DATABASE "+pgx.Identifier{building}.Sanitize()+" RENAME TO "+
		pgx.Identifier{name}.Sanitize()); err != nil {
		_, _ = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{building}.Sanitize()+" WITH (FORCE)")
		var nowExists bool
		if checkErr := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
			name).Scan(&nowExists); checkErr == nil && nowExists {
			return name, nil
		}
		return "", fmt.Errorf("store: naming a derived test template: %w", err)
	}
	return name, nil
}

// Name is the database's name.
func (d *TestDatabase) Name() string { return d.name }

// Drop drops the database by its exact name, and its read role when the role is its own: a role
// shared with the template's other clones is the template's, kept with it.
func (d *TestDatabase) Drop(ctx context.Context) error {
	started := time.Now()
	if !d.created.IsZero() {
		recordTimingFor(d.pkg, TimingLifetime, started.Sub(d.created))
		defer func() { recordTimingFor(d.pkg, TimingDrop, time.Since(started)) }()
	}
	db, err := adminPool(d.admin)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{d.name}.Sanitize()+
		" WITH (FORCE)"); err != nil {
		return fmt.Errorf("store: dropping the test database %s: %w", d.name, err)
	}
	if _, shared := forgetSharedReadRole(d.name); shared {
		return nil
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
	// The registry rows a template holds are the built-ins' declarations, so they are part of what it is.
	declarations, err := builtinDeclarationsText()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append(append(schema, []byte(testTemplateRevision)...), declarations...))
	name := "opnview_template_" + hex.EncodeToString(digest[:])[:16]
	db, err := adminPool(admin)
	if err != nil {
		return "", err
	}
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).
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
	// The template's read role is named after the template, and every clone shares it.
	shareReadRole(building, sqldb.ReadRoleName(name))
	defer forgetSharedReadRole(building)
	dsn, err := withDatabase(admin, building)
	if err != nil {
		return "", err
	}
	store, err := Open(ctx, dsn)
	if err != nil {
		return "", fmt.Errorf("store: building the test template: %w", err)
	}
	// The registry rows the built-ins write when they load: the DDL seeds none (schema.sql).
	if err := store.RegisterBuiltinDeclarations(ctx); err != nil {
		_ = store.Close()
		return "", fmt.Errorf("store: registering the built-ins in the test template: %w", err)
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
		if checkErr := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
			name).Scan(&nowExists); checkErr == nil && nowExists {
			return name, nil
		}
		return "", fmt.Errorf("store: naming the test template: %w", err)
	}
	return name, nil
}

// adminPool is the process's pool on the test instance's maintenance database, shared by every clone
// and every drop: one pool a process, kept open, rather than a new connection for each database
// created and each dropped.
func adminPool(admin string) (*sql.DB, error) {
	adminPools.mutex.Lock()
	defer adminPools.mutex.Unlock()
	if pool, found := adminPools.byDSN[admin]; found {
		return pool, nil
	}
	pool, err := sqldb.OpenPool(admin)
	if err != nil {
		return nil, err
	}
	pool.SetMaxIdleConns(adminIdleConnections)
	adminPools.byDSN[admin] = pool
	return pool, nil
}

// adminIdleConnections is how many maintenance connections a process keeps between uses: a package's
// tests create and drop databases from as many goroutines as run in parallel.
const adminIdleConnections = 4

var adminPools = struct {
	mutex sync.Mutex
	byDSN map[string]*sql.DB
}{byDSN: map[string]*sql.DB{}}

// The bounded backoff of a clone whose template another session holds.
const (
	cloneRetryFirst   = 5 * time.Millisecond
	cloneRetryLongest = 200 * time.Millisecond
	cloneRetryBound   = 10 * time.Second
)

// createDatabase creates a database, from a template or from template1 (which carries the
// TimescaleDB extension), with byte-order text.
func createDatabase(ctx context.Context, admin, name, template string) error {
	db, err := adminPool(admin)
	if err != nil {
		return err
	}
	statement := "CREATE DATABASE " + pgx.Identifier{name}.Sanitize()
	if template != "" {
		strategy, err := cloneStrategy()
		if err != nil {
			return err
		}
		statement += " TEMPLATE " + pgx.Identifier{template}.Sanitize() + " STRATEGY " + strategy
	} else {
		statement += " ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C' TEMPLATE template0"
	}
	// 55006, the template being accessed by another session (a concurrent clone), is retried after a
	// pause that doubles from cloneRetryFirst up to cloneRetryLongest, for at most cloneRetryBound in
	// all: a busy template is a matter of milliseconds, and a retry that does not wait only adds to
	// the contention it meets.
	var lastErr error
	pause := cloneRetryFirst
	deadline := time.Now().Add(cloneRetryBound)
	for {
		_, lastErr = db.ExecContext(ctx, statement)
		if lastErr == nil {
			if template == "" {
				return installExtension(ctx, admin, name)
			}
			return nil
		}
		if !strings.Contains(lastErr.Error(), "55006") && !strings.Contains(lastErr.Error(), "being accessed") {
			break
		}
		if time.Now().Add(pause).After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("store: creating the test database %s: %w", name, ctx.Err())
		case <-time.After(pause):
		}
		pause = min(2*pause, cloneRetryLongest)
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

// THE TEST DATABASES OF ONE TEMPLATE SHARE ITS READ ROLE (orchestrator decision 1 of Lot 2 of the
// plugin-API amendment). A role of its own cost each test database about 160 ms of database time --
// creating it, altering it, granting it, dropping it, each a write to the instance-wide role catalogue
// every concurrent test contends on. The template's role is created and granted once, when the template
// is built; a clone copies the grants with its catalogue, so its open writes nothing to the role catalogue
// and its drop leaves the role to the template. Each test still reads only its own database: a role
// connects to one database at a time, and holds nothing in another the test did not create.
var sharedReadRoles = struct {
	mutex  sync.Mutex
	byName map[string]string
}{byName: map[string]string{}}

// shareReadRole records that a test database's read connections log in as a template's role.
func shareReadRole(database, role string) {
	sharedReadRoles.mutex.Lock()
	defer sharedReadRoles.mutex.Unlock()
	sharedReadRoles.byName[database] = role
}

// sharedReadRole is the shared read role a test database logs in as, when it has one.
func sharedReadRole(database string) (string, bool) {
	sharedReadRoles.mutex.Lock()
	defer sharedReadRoles.mutex.Unlock()
	role, shared := sharedReadRoles.byName[database]
	return role, shared
}

// forgetSharedReadRole removes a database's entry, reporting whether it had one.
func forgetSharedReadRole(database string) (string, bool) {
	sharedReadRoles.mutex.Lock()
	defer sharedReadRoles.mutex.Unlock()
	role, shared := sharedReadRoles.byName[database]
	delete(sharedReadRoles.byName, database)
	return role, shared
}

// openReaders opens a database's read connections: as its own read role, which the product always
// uses, or as the shared role of the test template it was cloned from.
func openReaders(ctx context.Context, handle *sqldb.Handle) (*sqldb.ReadConnections, error) {
	var database string
	if err := handle.SQL.QueryRowContext(ctx, "SELECT current_database()").Scan(&database); err != nil {
		return nil, fmt.Errorf("store: reading the database name: %w", err)
	}
	if role, shared := sharedReadRole(database); shared {
		return sqldb.OpenSharedReaders(ctx, handle, role)
	}
	return sqldb.OpenReaders(ctx, handle)
}
