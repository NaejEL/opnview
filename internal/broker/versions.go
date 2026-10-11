package broker

import (
	"strings"
)

// The firewall's OPNsense version (A19 of the broker amendment): discovery reads it at every pass
// (docs/opnsense-api-survey.md, "Firewall versions, read from source for A19"), the broker holds
// it, a plugin's version ranges are selected against it, and the mirror records which version
// produced which shape.

// SetFirewallVersion records the firewall's version as discovery read it. A changed version is
// seen by the next call of every plugin, with no restart.
func (b *Broker) SetFirewallVersion(version string) {
	b.mutex.Lock()
	b.version = version
	b.mutex.Unlock()
	if b.mirror != nil {
		b.mirror.SetFirewallVersion(version)
	}
}

// FirewallVersion is the firewall's version, and whether it has been read.
func (b *Broker) FirewallVersion() (string, bool) {
	if b == nil {
		return "", false
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return b.version, b.version != ""
}

// ProductVersion reads the version out of the first entry systemInformation lists,
// "<product name> <version>-<architecture>": the last word, up to its last hyphen. It answers
// false for an entry of another shape.
func ProductVersion(entry string) (string, bool) {
	fields := strings.Fields(entry)
	if len(fields) < 2 {
		return "", false
	}
	last := fields[len(fields)-1]
	cut := strings.LastIndex(last, "-")
	if cut <= 0 {
		return "", false
	}
	version := last[:cut]
	if version == "" || version[0] < '0' || version[0] > '9' {
		return "", false
	}
	return version, true
}
