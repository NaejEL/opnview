package sources_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one. A JSON
// answer carries each as its closed code (web.ConditionOf).
type messageKey = web.MessageKey

const (
	msgAvailabilityHealthy       messageKey = "state.availability.healthy"
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
	msgAvailabilityNotYetProbed  messageKey = "state.availability.not_yet_probed"
	msgAvailabilityUnavailable   messageKey = "state.availability.unavailable"
	msgAvailabilityUnknown       messageKey = "state.availability.unknown"
	msgOutageProvider            messageKey = "state.outage.provider"
	msgPageIDSAdministration     messageKey = "plugin.suricata.opnsense_page.ids_administration"
	msgPageUnboundGeneral        messageKey = "plugin.unbound.opnsense_page.unbound_general"
	msgPageUnboundReporting      messageKey = "plugin.unbound.opnsense_page.unbound_reporting"
	msgSelectionAuto             messageKey = "state.sizing.selection_auto"
	msgSelectionOn               messageKey = "state.sizing.selection_on"
)
