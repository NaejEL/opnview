package sources

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string;
// the words are in locale/en.json beside this file, or in the core's locale when another
// surface uses the key too.

const (
	msgAvailabilityHealthy       messageKey = "state.availability.healthy"
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
	msgAvailabilityNotYetProbed  messageKey = "state.availability.not_yet_probed"
	msgAvailabilityUnavailable   messageKey = "state.availability.unavailable"
	msgAvailabilityUnknown       messageKey = "state.availability.unknown"
	msgNeverChecked              messageKey = "state.source.never_checked"
	msgNo                        messageKey = "state.no"
	msgNoDetail                  messageKey = "state.source.no_detail"
	msgOutageKind                messageKey = "state.outage.kind"
	msgOutageProvider            messageKey = "state.outage.provider"
	// THE SELECTION IS NOT THE AVAILABILITY, and the two are reported as two rows
	// because they are two facts: what the firewall says, and what the operator has
	// decided. internal/config says so where the selection is defined, and a surface
	// collapsing them would hide an operator's own decision behind a probe result.
	msgSelectionAuto messageKey = "state.sizing.selection_auto"
	msgSelectionOff  messageKey = "state.sizing.selection_off"
	msgSelectionOn   messageKey = "state.sizing.selection_on"
	msgTitleSources  messageKey = "sources.title"
	msgYes           messageKey = "state.yes"
)

// keys is every key above, so a test can assert that each exists in the catalogue and that
// no catalogue entry is unreachable. The OPNsense pages a provider links to are named in its plugin's
// own catalogue.
func keys() []messageKey {
	return []messageKey{
		msgAvailabilityHealthy,
		msgAvailabilityNotCollecting,
		msgAvailabilityNotYetProbed,
		msgAvailabilityUnavailable,
		msgAvailabilityUnknown,
		msgNeverChecked,
		msgNo,
		msgNoDetail,
		msgOutageKind,
		msgOutageProvider,
		msgSelectionAuto,
		msgSelectionOff,
		msgSelectionOn,
		msgTitleSources,
		msgYes,
	}
}
