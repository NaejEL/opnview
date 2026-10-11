package widgets

import (
	"os"
	"testing"

	"github.com/NaejEL/opnview/internal/store/storetest"
)

// TestMain drops the fixture templates the tests built once (storetest.Fixture).
func TestMain(m *testing.M) {
	code := m.Run()
	storetest.DropFixtures()
	os.Exit(code)
}
