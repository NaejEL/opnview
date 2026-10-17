package provider

import "sort"

// DiscoveryView is the discovery snapshot as a script reads it, wherever a kind's contract hands its
// functions `discovery`: the identifiers it may pass to an endpoint taking interface names, the devices
// it must never pass there, and the device each identifier runs on.
func DiscoveryView(snapshot Discovery) map[string]any {
	identifiers := make([]any, 0, len(snapshot.Identifiers))
	for _, identifier := range snapshot.Identifiers {
		identifiers = append(identifiers, string(identifier))
	}
	names := make([]string, 0, len(snapshot.Devices))
	for device := range snapshot.Devices {
		names = append(names, device)
	}
	sort.Strings(names)
	devices := make([]any, 0, len(names))
	for _, device := range names {
		devices = append(devices, device)
	}
	return map[string]any{
		"identifiers": identifiers,
		"devices":     devices,
		"isDevice": func(name string) bool {
			_, found := snapshot.Devices[name]
			return found
		},
		"isIdentifier": func(name string) bool {
			_, found := snapshot.InterfaceIDByIdentifier[name]
			return found
		},
		// deviceOf is the network device an identifier runs on, or "" when discovery does not
		// know the identifier.
		"deviceOf": func(identifier string) string {
			id, known := snapshot.InterfaceIDByIdentifier[identifier]
			if !known {
				return ""
			}
			for device, deviceID := range snapshot.InterfaceIDByDevice {
				if deviceID == id {
					return device
				}
			}
			return ""
		},
	}
}
