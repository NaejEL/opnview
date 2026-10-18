package messages

// Key is a catalogue key. A handler chooses a key, never a string, and a template resolves it; its
// own type keeps a plain string from being handed to a template where a key belongs. internal/web
// names it MessageKey.
type Key string

// The catalogue keys the core chooses.
//
// A HANDLER CHOOSES A KEY, NEVER A STRING. Every one of these is a catalogue key and
// nothing else, so the only place a reader's words exist is a locale file. A test asserts
// that each of these keys exists in the catalogue and that every catalogue entry is reached
// from a Go list of keys or from a template, so an entry nobody uses and a key nobody wrote
// are both failures. The keys a surface chooses are the surface's own (Surface.Keys).

// The refusals the middleware gives, whichever surface the request was for.
const (
	// MsgSetupClosed refuses the setup surface once an account exists.
	MsgSetupClosed Key = "error.setup_closed"
	// MsgSessionExpired is the sentence for a session that ended, as opposed to none.
	MsgSessionExpired Key = "error.session_expired"
	// MsgFormTokenRefused turns away a mutating request with a missing or wrong form token.
	MsgFormTokenRefused Key = "error.form_token_refused"
	// MsgKeyFileRefused turns away a reset that did not present the key file.
	MsgKeyFileRefused Key = "error.key_file_refused"
	// MsgInternal is what a reader is told when opnview could not do something at all.
	MsgInternal Key = "error.internal"
)

// The units a duration is spelled and typed in, a count of one and any other count; and the
// refusals of a typed duration.
const (
	MsgUnitDay     Key = "unit.day.one"
	MsgUnitDays    Key = "unit.day.other"
	MsgUnitHour    Key = "unit.hour.one"
	MsgUnitHours   Key = "unit.hour.other"
	MsgUnitMinute  Key = "unit.minute.one"
	MsgUnitMinutes Key = "unit.minute.other"
	MsgUnitSecond  Key = "unit.second.one"
	MsgUnitSeconds Key = "unit.second.other"
	// MsgDurationUnitUnknown refuses a unit that is not one of the four.
	MsgDurationUnitUnknown Key = "error.duration_unit_unknown"
	// MsgCollectionIntervalTooLarge refuses an interval no duration can hold. It is a
	// refusal of its own rather than "not a number", because the figure IS a number
	// and telling the operator otherwise would send them looking for a typo.
	MsgCollectionIntervalTooLarge Key = "error.collection_interval_too_large"
	// MsgCollectionValueInvalid refuses an interval that is not a positive whole number.
	MsgCollectionValueInvalid Key = "error.collection_value_invalid"
)
