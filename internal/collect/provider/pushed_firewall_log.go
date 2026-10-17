package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// The firewall_log kind's rule for a pushed record: its identity. A pushed record's digest is the push
// prefix and the SHA-256 of what the message said and who said it -- header and message -- whatever the
// script said, so the same line delivered twice is stored once and the two paths' identities can never
// meet (PS9 of the push amendment).

// PushedDigestPrefix begins the identity of every firewall_log record a push path delivered: no pulled
// digest carries it, and the collector reads the newest pushed record by it.
const PushedDigestPrefix = "push:"

func init() {
	RegisterPushConversion("firewall_log", PushConversion{Identify: func(answer map[string]any, delivery PushDelivery) {
		answer["digest"] = PushedDigest(delivery.Fields)
	}})
}

// PushedDigest is a pushed record's identity, from the protocol fields it was decoded from.
func PushedDigest(fields map[string]any) string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	var text strings.Builder
	for _, name := range names {
		fmt.Fprintf(&text, "%s=%v\x00", name, fields[name])
	}
	sum := sha256.Sum256([]byte(text.String()))
	return PushedDigestPrefix + hex.EncodeToString(sum[:])
}
