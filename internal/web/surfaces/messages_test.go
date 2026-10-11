package surfaces_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one.
type messageKey = web.MessageKey

const (
	msgInternal       messageKey = "error.internal"
	msgVerifyVerified messageKey = "state.verification.verified"
)
