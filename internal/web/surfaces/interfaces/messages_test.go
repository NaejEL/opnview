package interfaces_test

import "github.com/NaejEL/opnview/internal/web"

// The catalogue keys these tests name, written out here and not imported from the code under
// test: a test that read its keys from the code could not notice the code changing one. A JSON
// answer carries each as its closed code (web.ConditionOf).
type messageKey = web.MessageKey

const (
	msgCountsNoLongerDetected messageKey = "state.counts.no_longer_detected"
	msgCountsNoRemoved        messageKey = "state.counts.removed"
	msgInterfaceUnknown       messageKey = "error.interface_unknown"
	msgInterfaceUpstream      messageKey = "error.interface_upstream"
	msgNetworkHostBits        messageKey = "error.network_host_bits"
	msgNetworkIPv4Mapped      messageKey = "error.network_ipv4_mapped"
	msgNetworkLinkLocal       messageKey = "error.network_link_local"
	msgNetworkMalformed       messageKey = "error.network_malformed"
	msgNetworkUnknown         messageKey = "error.network_unknown"
)
