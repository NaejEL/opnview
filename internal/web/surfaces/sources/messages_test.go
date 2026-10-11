package sources_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one.
type messageKey = web.MessageKey

const (
	msgAvailabilityHealthy       messageKey = "state.availability.healthy"
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
	msgAvailabilityNotYetProbed  messageKey = "state.availability.not_yet_probed"
	msgAvailabilityUnavailable   messageKey = "state.availability.unavailable"
	msgAvailabilityUnknown       messageKey = "state.availability.unknown"
	msgLabelSourceState          messageKey = "collection.label.source_state"
	msgNeverChecked              messageKey = "state.source.never_checked"
	msgNo                        messageKey = "state.no"
	msgNoDetail                  messageKey = "state.source.no_detail"
	msgOpenInOPNsense            messageKey = "sources.label.opnsense_page"
	msgOutageKind                messageKey = "state.outage.kind"
	msgOutageProvider            messageKey = "state.outage.provider"
	msgPageIDSAdministration     messageKey = "opnsense_page.ids_administration"
	msgPageUnboundGeneral        messageKey = "opnsense_page.unbound_general"
	msgPageUnboundReporting      messageKey = "opnsense_page.unbound_reporting"
	msgSelectionAuto             messageKey = "state.sizing.selection_auto"
	msgSelectionOn               messageKey = "state.sizing.selection_on"
	msgUnitMinute                messageKey = "unit.minute.one"
	msgYes                       messageKey = "state.yes"
)
