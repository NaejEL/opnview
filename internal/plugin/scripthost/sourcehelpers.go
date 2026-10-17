package scripthost

import "github.com/NaejEL/opnview/internal/decode"

// The source helpers a script reaches under host.dhcp_lease and host.dns_lookup (docs/plugins.md,
// "Helpers"). They decode what the surveyed backends answer; PA11 of the plugin-API amendment moves
// each into the plugins that read those backends, and until then they live beside the host that
// exports them, and nowhere in the core.

// decodeLease decodes one lease row of any surveyed backend into a dhcp_lease_record, or false for a
// row with no address, which names no machine and keys nothing.
func decodeLease(row decode.Object) (map[string]any, bool) {
	address, present := decode.String(row, "address")
	if !present {
		return nil, false
	}
	mac := decode.MACPointer(decode.RawString(row, "hwaddr"))
	if mac == nil {
		// `mac` is the legacy plugin's spelling of the same field, and normalising the two names is
		// the only normalisation the survey says leases need.
		mac = decode.MACPointer(decode.RawString(row, "mac"))
	}
	expiresAt := decode.EpochPointer(row, "expire")
	if expiresAt == nil {
		expiresAt = decode.EpochPointer(row, "ends")
	}
	text := func(value *string) any {
		if value == nil {
			return nil
		}
		return *value
	}
	number := func(value *int64) any {
		if value == nil {
			return nil
		}
		return *value
	}
	return map[string]any{
		"address": address, "mac": text(mac), "hostname": text(decode.StringPointer(row, "hostname")),
		"dhcp_client_id": text(decode.StringPointer(row, "client_id")), "duid": text(decode.StringPointer(row, "duid")),
		"iaid": text(decode.StringPointer(row, "iaid")), "vendor_hint": text(decode.StringPointer(row, "mac_info")),
		"lease_state": normaliseLeaseState(row), "interface_identifier": decode.RawString(row, "if_name"),
		"interface_device": decode.RawString(row, "device"), "starts_at": nil, "expires_at": number(expiresAt),
	}, true
}

// normaliseLeaseState maps the backends' lease states onto the closed vocabulary of the contract.
func normaliseLeaseState(row decode.Object) string {
	if reserved := decode.Flag(row, "is_reserved"); reserved != nil && *reserved {
		return "reserved"
	}
	for _, key := range []string{"state", "lease_type", "status"} {
		value, present := decode.String(row, key)
		if !present {
			continue
		}
		switch decode.LowerASCII(value) {
		case "active", "assigned", "1":
			return "active"
		case "expired", "released", "free", "backup", "0":
			return "expired"
		case "reserved", "static":
			return "reserved"
		}
	}
	return "unknown"
}

// normaliseAction maps a resolver's action onto the closed vocabulary the contract holds. Anything
// else is `unknown`: a lookup whose verdict opnview cannot name must not be counted as allowed.
func normaliseAction(raw string) string {
	switch decode.LowerASCII(raw) {
	case "pass", "passed", "allowed":
		return "pass"
	case "block", "blocked":
		return "block"
	case "drop", "dropped":
		return "drop"
	default:
		return "unknown"
	}
}
