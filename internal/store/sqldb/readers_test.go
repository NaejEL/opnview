package sqldb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NaejEL/opnview/internal/store/sqldb"
	"github.com/NaejEL/opnview/internal/store/storetest"
)

// TestTheReadRoleCannotReadASecretNorRaiseItself is P-AC12's second and third halves: each object
// the read role holds nothing on fails with a permission error when read directly on the read
// connections, and no function called inside a SELECT gives the role what it does not hold.
func TestTheReadRoleCannotReadASecretNorRaiseItself(t *testing.T) {
	t.Parallel()
	database := storetest.Open(t)
	ctx := context.Background()
	for _, object := range sqldb.UnreadableObjects {
		t.Run(object, func(t *testing.T) {
			if _, err := database.Reader.DB.QueryContext(ctx, "SELECT * FROM "+object); err == nil ||
				!strings.Contains(err.Error(), "42501") {
				t.Errorf("reading %s on the read connections gave %v, not a permission error", object, err)
			}
		})
	}
	for name, statement := range map[string]string{
		"taking another role":    "SELECT set_config('role', 'postgres', false)",
		"reading a server file":  "SELECT pg_read_file('postgresql.conf')",
		"listing a directory":    "SELECT pg_ls_dir('.')",
		"reading through a view": "SELECT count(*) FROM pg_catalog.pg_authid",
	} {
		rows, err := database.Reader.DB.QueryContext(ctx, statement)
		if err == nil {
			err = rows.Err()
			for rows.Next() {
			}
			_ = rows.Close()
		}
		if err == nil {
			t.Errorf("%s succeeded on the read connections", name)
		}
	}
	var role string
	if err := database.Reader.DB.QueryRowContext(ctx, "SELECT current_user").Scan(&role); err != nil ||
		role != database.Reader.Role() {
		t.Errorf("the read connections run as %q (%v)", role, err)
	}
}
