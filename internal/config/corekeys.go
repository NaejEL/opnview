package config

import "strings"

// CoreKeys are the setting keys the core itself reads. A setting a plugin declares is stored under
// <plugin>_<key>, and a composed key that equals one of these, or begins with a prefix the core
// composes keys under, is refused at load: a plugin never shadows a setting of the core's.
func CoreKeys() []string {
	return []string{
		KeyRetentionSeconds, KeyAggregateMode, KeyFirewallLogInterval, KeySecurityEventInterval,
		KeyMeasurementInterval, KeyDHCPLeaseInterval, KeyDNSLookupInterval, KeyResolverCacheInterval,
		KeyResolverLocalDataInterval, KeyDiscoveryInterval, KeyPurgeInterval, KeyGeoIPRefreshInterval,
		KeyGeoLookupInterval, KeyPublicSuffixRefreshInterval, KeyAttributionMaxDelay, KeyCacheAnswerDelay,
		KeyLegPairingWindow, KeyLeaseBackendFailurePassLimit, KeyHeartbeatInterval, KeyDowntimeThreshold,
		KeyFirewallLogPageSize, KeySecurityEventPageSize, KeyDHCPLeasePageSize,
		KeyOPNsenseBaseURL, KeyOPNsenseAPIKey, KeyOPNsenseCertificateFingerprint, KeyTheme,
		KeyLiveInterestSeconds, KeyLiveSampleRetentionSeconds,
	}
}

// IsCoreKey reports whether a setting key is the core's: one of CoreKeys, or one the core composes
// from a registry row (a source selection).
func IsCoreKey(key string) bool {
	if strings.HasPrefix(key, "source_selection_") {
		return true
	}
	for _, core := range CoreKeys() {
		if core == key {
			return true
		}
	}
	return false
}
