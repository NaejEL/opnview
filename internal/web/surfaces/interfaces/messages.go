package interfaces

// The catalogue keys this surface's code chooses. A handler chooses a key, never a string;
// the words are in locale/en.json beside this file, or in the core's locale when another
// surface uses the key too.

const (
	msgCountsNoLongerDetected messageKey = "state.counts.no_longer_detected"
	msgCountsNoRemoved        messageKey = "state.counts.removed"
	msgInterfaceActionUnknown messageKey = "error.interface_action_unknown"
	// msgInterfaceSaved confirms an edit, and says when it takes effect: at the
	// collector's next derivation, never "reclassified".
	msgInterfaceSaved    messageKey = "notice.interface_saved"
	msgInterfaceUnknown  messageKey = "error.interface_unknown"
	msgInterfaceUpstream messageKey = "error.interface_upstream"
	msgLinkLocalInvalid  messageKey = "error.link_local_invalid"
	msgNetworkHostBits   messageKey = "error.network_host_bits"
	msgNetworkIPv4Mapped messageKey = "error.network_ipv4_mapped"
	msgNetworkLinkLocal  messageKey = "error.network_link_local"
	// The refusals of a typed network, each its own sentence.
	msgNetworkMalformed messageKey = "error.network_malformed"
	msgNetworkUnknown   messageKey = "error.network_unknown"
	msgNo               messageKey = "state.no"
	msgOriginDetected   messageKey = "state.origin.detected"
	msgOriginOperator   messageKey = "state.origin.operator"
	msgTitleInterfaces  messageKey = "interfaces.title"
	msgYes              messageKey = "state.yes"
)

// keys is every key above, so a test can assert that each exists in the catalogue and that
// no catalogue entry is unreachable.
func keys() []messageKey {
	return []messageKey{
		msgCountsNoLongerDetected,
		msgCountsNoRemoved,
		msgInterfaceActionUnknown,
		msgInterfaceSaved,
		msgInterfaceUnknown,
		msgInterfaceUpstream,
		msgLinkLocalInvalid,
		msgNetworkHostBits,
		msgNetworkIPv4Mapped,
		msgNetworkLinkLocal,
		msgNetworkMalformed,
		msgNetworkUnknown,
		msgNo,
		msgOriginDetected,
		msgOriginOperator,
		msgTitleInterfaces,
		msgYes,
	}
}
