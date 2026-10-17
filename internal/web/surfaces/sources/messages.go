package sources

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string; a
// JSON answer carries its closed code (web.ConditionOf), which the front translates through the
// catalogue.

const (
	msgAvailabilityNotCollecting messageKey = "state.availability.not_collecting"
	msgPageDnsmasqDHCPRanges     messageKey = "opnsense_page.dnsmasq_dhcp_ranges"
	msgPageIDSAdministration     messageKey = "opnsense_page.ids_administration"
	msgPageKeaDHCPv4             messageKey = "opnsense_page.kea_dhcpv4"
	msgPageNetFlow               messageKey = "opnsense_page.netflow"
	msgPageUnboundGeneral        messageKey = "opnsense_page.unbound_general"
	msgPageUnboundReporting      messageKey = "opnsense_page.unbound_reporting"
)

// keys is every key above, so a test can assert that each exists in the catalogue.
func keys() []messageKey {
	return []messageKey{
		msgAvailabilityNotCollecting,
		msgPageDnsmasqDHCPRanges,
		msgPageIDSAdministration,
		msgPageKeaDHCPv4,
		msgPageNetFlow,
		msgPageUnboundGeneral,
		msgPageUnboundReporting,
	}
}
