package settings_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one. A JSON
// answer carries each as its closed code (web.ConditionOf).
type messageKey = web.MessageKey

const (
	msgCredentialAbsent        messageKey = "state.credential.absent"
	msgCredentialReady         messageKey = "state.credential.ready"
	msgCredentialUndecryptable messageKey = "state.credential.undecryptable"
	msgVerifyCredentialsBad    messageKey = "state.verification.credentials_rejected"
	msgVerifyEndpointNotFound  messageKey = "state.verification.endpoint_not_found"
	msgVerifyUnreachable       messageKey = "state.verification.unreachable"
	msgVerifyVerified          messageKey = "state.verification.verified"
)
