package netflow

import (
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/push/ipfixregistry"
)

// Field values, typed from the registry's abstract data type (RFC 7011, section 6.1), and the
// mapping of each abstract type onto the plugin type vocabulary (PS4 of the push amendment).

// columnTypeOf maps an abstract data type onto the plugin type vocabulary: unsigned* and signed* are
// integer, float* real, the address types ip, macAddress mac, dateTime* instant, string text and
// boolean boolean; octetArray and every other type (unsigned256, the list types) is text in
// lower-case hexadecimal.
func columnTypeOf(abstract string) string {
	switch {
	case strings.HasPrefix(abstract, "unsigned") && abstract != "unsigned256",
		strings.HasPrefix(abstract, "signed"):
		return push.TypeInteger
	case strings.HasPrefix(abstract, "float"):
		return push.TypeReal
	case abstract == ipfixregistry.TypeIPv4Address || abstract == ipfixregistry.TypeIPv6Address:
		return push.TypeIP
	case abstract == ipfixregistry.TypeMACAddress:
		return push.TypeMAC
	case strings.HasPrefix(abstract, "dateTime"):
		return push.TypeInstant
	case abstract == ipfixregistry.TypeString:
		return push.TypeText
	case abstract == ipfixregistry.TypeBoolean:
		return push.TypeBoolean
	}
	return push.TypeText
}

// ntpEpochOffset is the seconds between the NTP era 0 epoch (1900) and the Unix epoch, for the
// dateTimeMicroseconds and dateTimeNanoseconds encoding (RFC 7011, section 6.1.9).
const ntpEpochOffset = 2208988800

// decodeValue decodes one field's octets by its abstract type. A length the type cannot have is an
// error: the record is then skipped for the subscriber that needs the field, never guessed at.
func decodeValue(abstract string, octets []byte) (any, error) {
	switch {
	case strings.HasPrefix(abstract, "unsigned") && abstract != "unsigned256":
		if len(octets) == 0 || len(octets) > 8 {
			return nil, fmt.Errorf("an %s of %d octets", abstract, len(octets))
		}
		var value uint64
		for _, b := range octets {
			value = value<<8 | uint64(b)
		}
		return value, nil
	case strings.HasPrefix(abstract, "signed"):
		if len(octets) == 0 || len(octets) > 8 {
			return nil, fmt.Errorf("a %s of %d octets", abstract, len(octets))
		}
		var value uint64
		for _, b := range octets {
			value = value<<8 | uint64(b)
		}
		shift := uint(64 - 8*len(octets))
		return int64(value<<shift) >> shift, nil
	case abstract == ipfixregistry.TypeFloat32 || abstract == ipfixregistry.TypeFloat64:
		switch len(octets) {
		case 4:
			return float64(math.Float32frombits(binary.BigEndian.Uint32(octets))), nil
		case 8:
			return math.Float64frombits(binary.BigEndian.Uint64(octets)), nil
		}
		return nil, fmt.Errorf("a float of %d octets", len(octets))
	case abstract == ipfixregistry.TypeBoolean:
		if len(octets) != 1 {
			return nil, fmt.Errorf("a boolean of %d octets", len(octets))
		}
		switch octets[0] {
		case 1:
			return true, nil
		case 2:
			return false, nil
		}
		return nil, fmt.Errorf("the boolean value %d, neither 1 nor 2", octets[0])
	case abstract == ipfixregistry.TypeMACAddress:
		if len(octets) != 6 {
			return nil, fmt.Errorf("a MAC address of %d octets", len(octets))
		}
		return net.HardwareAddr(append([]byte(nil), octets...)), nil
	case abstract == ipfixregistry.TypeIPv4Address:
		if len(octets) != 4 {
			return nil, fmt.Errorf("an IPv4 address of %d octets", len(octets))
		}
		return netip.AddrFrom4([4]byte(octets)), nil
	case abstract == ipfixregistry.TypeIPv6Address:
		if len(octets) != 16 {
			return nil, fmt.Errorf("an IPv6 address of %d octets", len(octets))
		}
		return netip.AddrFrom16([16]byte(octets)).Unmap(), nil
	case abstract == ipfixregistry.TypeString:
		if !utf8.Valid(octets) {
			return string(bytesToValidUTF8(octets)), nil
		}
		return string(octets), nil
	case abstract == ipfixregistry.TypeDateTimeSeconds:
		if len(octets) != 4 {
			return nil, fmt.Errorf("a dateTimeSeconds of %d octets", len(octets))
		}
		return time.Unix(int64(binary.BigEndian.Uint32(octets)), 0).UTC(), nil
	case abstract == ipfixregistry.TypeDateTimeMilliseconds:
		if len(octets) != 8 {
			return nil, fmt.Errorf("a dateTimeMilliseconds of %d octets", len(octets))
		}
		return time.UnixMilli(int64(binary.BigEndian.Uint64(octets))).UTC(), nil
	case abstract == ipfixregistry.TypeDateTimeMicroseconds || abstract == ipfixregistry.TypeDateTimeNanoseconds:
		if len(octets) != 8 {
			return nil, fmt.Errorf("an NTP timestamp of %d octets", len(octets))
		}
		seconds := int64(binary.BigEndian.Uint32(octets[:4])) - ntpEpochOffset
		fraction := uint64(binary.BigEndian.Uint32(octets[4:]))
		if abstract == ipfixregistry.TypeDateTimeMicroseconds {
			// The 11 least significant bits are not used (RFC 7011, section 6.1.9).
			fraction &^= 0x7FF
		}
		nanoseconds := int64(fraction * 1_000_000_000 >> 32)
		return time.Unix(seconds, nanoseconds).UTC(), nil
	}
	return append([]byte(nil), octets...), nil
}

func bytesToValidUTF8(octets []byte) []byte {
	var out []byte
	for len(octets) > 0 {
		r, size := utf8.DecodeRune(octets)
		if r == utf8.RuneError && size <= 1 {
			out = append(out, "�"...)
		} else {
			out = append(out, octets[:size]...)
		}
		octets = octets[size:]
	}
	return out
}
