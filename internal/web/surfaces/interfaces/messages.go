package interfaces

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string; a
// JSON answer carries its closed code (web.ConditionOf), which the front translates through the
// catalogue.

const (
	msgCountsNoLongerDetected messageKey = "state.counts.no_longer_detected"
	msgCountsNoRemoved        messageKey = "state.counts.removed"
	msgInterfaceActionUnknown messageKey = "error.interface_action_unknown"
	msgInterfaceUnknown       messageKey = "error.interface_unknown"
	msgInterfaceUpstream      messageKey = "error.interface_upstream"
	msgLinkLocalInvalid       messageKey = "error.link_local_invalid"
	msgNetworkHostBits        messageKey = "error.network_host_bits"
	msgNetworkIPv4Mapped      messageKey = "error.network_ipv4_mapped"
	msgNetworkLinkLocal       messageKey = "error.network_link_local"
	// The refusals of a typed network, each its own code.
	msgNetworkMalformed messageKey = "error.network_malformed"
	msgNetworkUnknown   messageKey = "error.network_unknown"
	msgOriginDetected   messageKey = "state.origin.detected"
	msgOriginOperator   messageKey = "state.origin.operator"
)

// keys is every key above, so a test can assert that each exists in the catalogue.
func keys() []messageKey {
	return []messageKey{
		msgCountsNoLongerDetected,
		msgCountsNoRemoved,
		msgInterfaceActionUnknown,
		msgInterfaceUnknown,
		msgInterfaceUpstream,
		msgLinkLocalInvalid,
		msgNetworkHostBits,
		msgNetworkIPv4Mapped,
		msgNetworkLinkLocal,
		msgNetworkMalformed,
		msgNetworkUnknown,
		msgOriginDetected,
		msgOriginOperator,
	}
}
