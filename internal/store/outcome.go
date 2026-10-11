package store

import (
	"context"
	"fmt"
)

// AvailabilityOutcome is what the last probe or read of one provider turned into, named
// from a closed set (SPEC-step-5b-widget-api.md, D1).
//
// IT EXISTS SO THAT NOTHING READS `detail`. The state says whether a source can be read;
// the detail says why in words a person reads; the outcome says why in a word a program
// reads. An interface translates the outcome, and a reader that wanted to tell a refused
// key from an absent module had, before this column, nothing but an English sentence to
// parse. The detail stays, verbatim, as diagnostic text.
//
// The set is derived from what the collectors already distinguished in their details, one
// code per distinction, and no code was invented for a case no collector reports. The
// schema's CHECK on source_availability.outcome holds the same set; a test compares the
// two, and docs/data-model.md, `source_availability`, states each code's meaning.
type AvailabilityOutcome string

// The outcomes. The comment on each is its documented meaning.
const (
	// OutcomeNotYetProbed is the row the schema creates: nothing has asked yet.
	OutcomeNotYetProbed AvailabilityOutcome = "not_yet_probed"
	// OutcomeHealthy is a probe or read that answered and was read.
	OutcomeHealthy AvailabilityOutcome = "healthy"
	// OutcomeHealthyEmpty is a read that answered and returned no record: reachable and
	// silent, which is not an absence of activity.
	OutcomeHealthyEmpty AvailabilityOutcome = "healthy_empty"
	// OutcomePartialReadings is a sampling pass that answered while some of its readings
	// did not; the detail names them.
	OutcomePartialReadings AvailabilityOutcome = "partial_readings"
	// OutcomePresentButDisabled is a component the firewall runs with the setting this
	// source needs switched off: a configuration state the user can change.
	OutcomePresentButDisabled AvailabilityOutcome = "present_but_disabled"
	// OutcomeServiceNotRunning is a service whose status endpoint reported something
	// other than running.
	OutcomeServiceNotRunning AvailabilityOutcome = "service_not_running"
	// OutcomeAuthenticationFailed is HTTP 401: the API key and secret were refused.
	OutcomeAuthenticationFailed AvailabilityOutcome = "authentication_failed"
	// OutcomeDenied is HTTP 403: the key's user lacks the privilege for the path.
	OutcomeDenied AvailabilityOutcome = "denied"
	// OutcomeNotFound is HTTP 404: the firewall does not answer the path.
	OutcomeNotFound AvailabilityOutcome = "not_found"
	// OutcomeServerError is any other unsuccessful HTTP status.
	OutcomeServerError AvailabilityOutcome = "server_error"
	// OutcomeEmptyBody is HTTP 200 with an empty body, a backend failure.
	OutcomeEmptyBody AvailabilityOutcome = "empty_body"
	// OutcomeResultFailed is HTTP 200 with OPNsense's {"result": "failed"} envelope.
	OutcomeResultFailed AvailabilityOutcome = "result_failed"
	// OutcomeUndecodable is an answer whose body is not the shape the endpoint returns.
	OutcomeUndecodable AvailabilityOutcome = "undecodable"
	// OutcomeStatusNotOK is an envelope whose `status` is not "ok".
	OutcomeStatusNotOK AvailabilityOutcome = "status_not_ok"
	// OutcomeTransportFailure is no answer at all: the firewall was not reached.
	OutcomeTransportFailure AvailabilityOutcome = "transport_failure"
	// OutcomeCertificateRefused is the firewall answering with a certificate opnview
	// does not accept.
	OutcomeCertificateRefused AvailabilityOutcome = "certificate_refused"
	// OutcomeRequestNotMade is a probe or read that ended before any request reached the
	// firewall: no URL configured, or the request refused by opnview's own guards.
	OutcomeRequestNotMade AvailabilityOutcome = "request_not_made"
	// OutcomePluginNotInstalled is the end-of-life DHCP plugin answering 404.
	OutcomePluginNotInstalled AvailabilityOutcome = "plugin_not_installed"
	// OutcomeReadNotSupported is an implementation that answers and whose material
	// opnview does not read, because its field contract is unverified.
	OutcomeReadNotSupported AvailabilityOutcome = "read_not_supported"
	// OutcomeNoCacheRead is a resolver that offers no cache read through the API.
	OutcomeNoCacheRead AvailabilityOutcome = "no_cache_read"
	// OutcomeNoLocalDataRead is a resolver that offers no local-data read through the API.
	OutcomeNoLocalDataRead AvailabilityOutcome = "no_local_data_read"
	// OutcomeLogGrammarNotRecorded is a resolver whose lookups are exposed only as free
	// text under a grammar nobody has recorded.
	OutcomeLogGrammarNotRecorded AvailabilityOutcome = "log_grammar_not_recorded"
	// OutcomeNoLicenceKey is the MaxMind refresh with no licence key stored.
	OutcomeNoLicenceKey AvailabilityOutcome = "no_licence_key"
	// OutcomeLicenceKeyUndecryptable is a stored licence key the key file cannot open.
	OutcomeLicenceKeyUndecryptable AvailabilityOutcome = "licence_key_undecryptable"
	// OutcomeNoAccountID is the MaxMind refresh with no account ID stored.
	OutcomeNoAccountID AvailabilityOutcome = "no_account_id"
	// OutcomeDownloadRefused is a download the server refused (credentials).
	OutcomeDownloadRefused AvailabilityOutcome = "download_refused"
	// OutcomeDownloadLimitReached is a download refused because the account's daily
	// limit is spent.
	OutcomeDownloadLimitReached AvailabilityOutcome = "download_limit_reached"
	// OutcomeDownloadFailed is a download that could not be completed.
	OutcomeDownloadFailed AvailabilityOutcome = "download_failed"
	// OutcomeDownloadMalformed is a download that fetched something that is not the
	// list.
	OutcomeDownloadMalformed AvailabilityOutcome = "download_malformed"
	// OutcomeStoreFailure is a read that answered and whose records opnview could not
	// store: the fault is opnview's database, not the source.
	OutcomeStoreFailure AvailabilityOutcome = "store_failure"
	// OutcomeScriptFailed is a plugin's script that raised an error, or failed in a way the
	// host caught, instead of answering (docs/plugins.md, "Failures"). Nothing it returned
	// is stored.
	OutcomeScriptFailed AvailabilityOutcome = "script_failed"
	// OutcomeScriptTimeout is a plugin's script that ran past the time a call may take and
	// was stopped. Nothing it returned is stored.
	OutcomeScriptTimeout AvailabilityOutcome = "script_timeout"
	// OutcomeRecordsInvalid is a plugin that answered with records its kind's contract, or
	// its own tables, do not admit. None of them is stored.
	OutcomeRecordsInvalid AvailabilityOutcome = "records_invalid"
	// OutcomeFirewallVersionUnsupported is a plugin that maps OPNsense version ranges to its routes or
	// its scripts, on a firewall whose version no range covers, or whose version is not known yet
	// (A19 of the broker amendment). The closest range is never guessed: nothing is called.
	OutcomeFirewallVersionUnsupported AvailabilityOutcome = "firewall_version_unsupported"
)

// AvailabilityOutcomes is the closed set, in the order docs/data-model.md lists it. The
// schema's CHECK holds exactly these values.
var AvailabilityOutcomes = []AvailabilityOutcome{
	OutcomeNotYetProbed, OutcomeHealthy, OutcomeHealthyEmpty, OutcomePartialReadings,
	OutcomePresentButDisabled, OutcomeServiceNotRunning,
	OutcomeAuthenticationFailed, OutcomeDenied, OutcomeNotFound, OutcomeServerError,
	OutcomeEmptyBody, OutcomeResultFailed, OutcomeUndecodable, OutcomeStatusNotOK,
	OutcomeTransportFailure, OutcomeCertificateRefused, OutcomeRequestNotMade,
	OutcomePluginNotInstalled, OutcomeReadNotSupported, OutcomeNoCacheRead,
	OutcomeNoLocalDataRead, OutcomeLogGrammarNotRecorded,
	OutcomeNoLicenceKey, OutcomeLicenceKeyUndecryptable, OutcomeNoAccountID,
	OutcomeDownloadRefused, OutcomeDownloadLimitReached, OutcomeDownloadFailed,
	OutcomeDownloadMalformed, OutcomeStoreFailure,
	OutcomeScriptFailed, OutcomeScriptTimeout, OutcomeRecordsInvalid, OutcomeFirewallVersionUnsupported,
}

// Valid reports whether the outcome is in the closed set.
func (o AvailabilityOutcome) Valid() bool {
	for _, known := range AvailabilityOutcomes {
		if o == known {
			return true
		}
	}
	return false
}

// AvailabilityOutcomeOf reads back one provider's outcome code.
func (s *Store) AvailabilityOutcomeOf(ctx context.Context, providerID int64) (AvailabilityOutcome, error) {
	var outcome string
	if err := s.db.QueryRowContext(ctx,
		"SELECT outcome FROM source_availability WHERE provider_id = ?", providerID).Scan(&outcome); err != nil {
		return "", fmt.Errorf("store: reading the availability outcome of provider %d: %w", providerID, err)
	}
	return AvailabilityOutcome(outcome), nil
}
