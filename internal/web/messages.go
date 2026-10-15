package web

// The catalogue keys the core chooses.
//
// A HANDLER CHOOSES A KEY, NEVER A STRING. Every one of these is a catalogue key and
// nothing else, so the only place a reader's words exist is a locale file. A test asserts
// that each of these keys exists in the catalogue and that every catalogue entry is reached
// from a Go list of keys or from a template, so an entry nobody uses and a key nobody wrote
// are both failures. The keys a surface chooses are the surface's own (Surface.Keys).

// The refusals the middleware gives, whichever surface the request was for.
const (
	// msgSetupClosed refuses the setup surface once an account exists.
	msgSetupClosed messageKey = "error.setup_closed"
	// msgSessionExpired is the sentence for a session that ended, as opposed to none.
	msgSessionExpired messageKey = "error.session_expired"
	// msgFormTokenRefused turns away a mutating request with a missing or wrong form token.
	msgFormTokenRefused messageKey = "error.form_token_refused"
	// msgKeyFileRefused turns away a reset that did not present the key file.
	msgKeyFileRefused messageKey = "error.key_file_refused"
	// msgInternal is what a reader is told when opnview could not do something at all.
	msgInternal messageKey = "error.internal"
)

// The units a duration is spelled and typed in, a count of one and any other count; and the
// refusals of a typed duration.
const (
	MsgUnitDay     messageKey = "unit.day.one"
	MsgUnitDays    messageKey = "unit.day.other"
	MsgUnitHour    messageKey = "unit.hour.one"
	MsgUnitHours   messageKey = "unit.hour.other"
	MsgUnitMinute  messageKey = "unit.minute.one"
	MsgUnitMinutes messageKey = "unit.minute.other"
	MsgUnitSecond  messageKey = "unit.second.one"
	MsgUnitSeconds messageKey = "unit.second.other"
	// msgDurationUnitUnknown refuses a unit that is not one of the four.
	MsgDurationUnitUnknown messageKey = "error.duration_unit_unknown"
	// msgCollectionIntervalTooLarge refuses an interval no duration can hold. It is a
	// refusal of its own rather than "not a number", because the figure IS a number
	// and telling the operator otherwise would send them looking for a typo.
	MsgCollectionIntervalTooLarge messageKey = "error.collection_interval_too_large"
	// msgCollectionValueInvalid refuses an interval that is not a positive whole number.
	MsgCollectionValueInvalid messageKey = "error.collection_value_invalid"
)

// The theme names. The theme is one setting row for the installation, so these
// are the three values that row may hold.
const (
	msgThemeSystem messageKey = "theme.system"
	msgThemeLight  messageKey = "theme.light"
	msgThemeDark   messageKey = "theme.dark"
)

// CoreKeys is every key the core's Go code chooses, in one place, so a test can assert that
// each exists in the catalogue and that no catalogue entry is unreachable. The registry's
// vocabulary (messages_registry.go) is included.
//
// It is a function rather than a variable so nothing can append to it at run time.
func CoreKeys() []MessageKey {
	return append([]MessageKey{
		msgSetupClosed, msgSessionExpired, msgFormTokenRefused, msgKeyFileRefused, msgInternal,
		MsgUnitDay, MsgUnitDays, MsgUnitHour, MsgUnitHours, MsgUnitMinute, MsgUnitMinutes,
		MsgUnitSecond, MsgUnitSeconds, MsgDurationUnitUnknown, MsgCollectionIntervalTooLarge, MsgCollectionValueInvalid,
		msgThemeSystem, msgThemeLight, msgThemeDark,
	}, append(vocabularyKeys(), availabilityKeys()...)...)
}
