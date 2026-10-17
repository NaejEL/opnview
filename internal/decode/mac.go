package decode

import "strings"

// NormaliseMAC returns a hardware address in its one stored form -- six lower-case hexadecimal
// octets separated by colons -- or false for anything else. The identity cascade keys a client on
// it, so it is the core's, applied to every record that carries one whichever source reported it.
func NormaliseMAC(raw string) (string, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if len(trimmed) != 17 {
		return "", false
	}
	for index := 0; index < len(trimmed); index++ {
		character := trimmed[index]
		if index%3 == 2 {
			if character != ':' {
				return "", false
			}
			continue
		}
		isDigit := character >= '0' && character <= '9'
		isHex := character >= 'a' && character <= 'f'
		if !isDigit && !isHex {
			return "", false
		}
	}
	return trimmed, true
}

// MACPointer is NormaliseMAC as an optional value: nil for a value that is not a hardware address,
// which is stored as absent rather than stored wrong.
func MACPointer(raw string) *string {
	normalised, ok := NormaliseMAC(raw)
	if !ok {
		return nil
	}
	return &normalised
}
