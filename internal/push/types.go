package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// The plugin type vocabulary (PS4 of the push amendment): the words a plugin table's column is
// declared with, push output table or not. They are words of a closed list, never SQL type
// strings; the store maps each to its PostgreSQL type (store.PluginColumnTypes).
const (
	TypeInteger = "integer"
	TypeReal    = "real"
	TypeText    = "text"
	TypeBoolean = "boolean"
	TypeIP      = "ip"
	TypeMAC     = "mac"
	TypeInstant = "instant"
	// TypeJSON is a document (ADR 0020 wave 5): the declared measures' parts a rollup row carries.
	TypeJSON = "json"
)

// ErrValueInvalid is a value that does not convert to its column's type: counted as
// push_value_invalid, never coerced.
var ErrValueInvalid = errors.New("push: the value does not convert to the column's type")

// Convert turns a decoded value -- a field of a decoded message, a regular expression's group, or a
// parse function's answer -- into the Go value of a column type:
//
//	integer  int64            real     float64          text  string
//	boolean  bool             ip       netip.Addr       mac   net.HardwareAddr
//	instant  time.Time (UTC)
//
// A value that does not convert is ErrValueInvalid. Nothing is wrapped, rounded or guessed: an
// unsigned 64-bit value above the signed range is refused rather than wrapped, a real with a
// fraction is not an integer, and text that is not an address is not an address.
func Convert(columnType string, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch columnType {
	case TypeInteger:
		return toInteger(value)
	case TypeReal:
		return toReal(value)
	case TypeText:
		return toText(value)
	case TypeBoolean:
		return toBoolean(value)
	case TypeIP:
		return toIP(value)
	case TypeMAC:
		return toMAC(value)
	case TypeInstant:
		return toInstant(value)
	case TypeJSON:
		return toJSON(value)
	}
	return nil, fmt.Errorf("push: %q is not a type of the vocabulary", columnType)
}

func invalid(value any, columnType string) error {
	return fmt.Errorf("%w: %T is not a(n) %s", ErrValueInvalid, value, columnType)
}

// toJSON is a document: text that is JSON as it is, any other value encoded.
func toJSON(value any) (any, error) {
	if typed, ok := value.(string); ok {
		if !json.Valid([]byte(typed)) {
			return nil, invalid(value, TypeJSON)
		}
		return typed, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, invalid(value, TypeJSON)
	}
	return string(encoded), nil
}

func toInteger(value any) (any, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case uint8:
		return int64(typed), nil
	case uint16:
		return int64(typed), nil
	case uint32:
		return int64(typed), nil
	case uint64:
		if typed > math.MaxInt64 {
			return nil, fmt.Errorf("%w: %d is above the signed 64-bit range", ErrValueInvalid, typed)
		}
		return int64(typed), nil
	case float64:
		if typed != math.Trunc(typed) || math.IsInf(typed, 0) || math.IsNaN(typed) ||
			typed > math.MaxInt64 || typed < math.MinInt64 {
			return nil, fmt.Errorf("%w: %v is not a whole number in range", ErrValueInvalid, typed)
		}
		return int64(typed), nil
	case bool:
		return nil, invalid(value, TypeInteger)
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not an integer", ErrValueInvalid, typed)
		}
		return parsed, nil
	}
	return nil, invalid(value, TypeInteger)
}

func toReal(value any) (any, error) {
	switch typed := value.(type) {
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return nil, fmt.Errorf("%w: %v is not a finite number", ErrValueInvalid, typed)
		}
		return typed, nil
	case float32:
		return toReal(float64(typed))
	case int64, int, int32, uint8, uint16, uint32, uint64:
		integer, err := toInteger(value)
		if err != nil {
			if unsigned, ok := value.(uint64); ok {
				return float64(unsigned), nil
			}
			return nil, err
		}
		return float64(integer.(int64)), nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return nil, fmt.Errorf("%w: %q is not a number", ErrValueInvalid, typed)
		}
		return parsed, nil
	}
	return nil, invalid(value, TypeReal)
}

func toText(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		// An octet array is text in lower-case hexadecimal (PS4).
		return fmt.Sprintf("%x", typed), nil
	case netip.Addr:
		return typed.String(), nil
	case net.HardwareAddr:
		return typed.String(), nil
	case int64, int, int32, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", typed), nil
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case time.Time:
		return typed.UTC().Format(time.RFC3339Nano), nil
	}
	return nil, invalid(value, TypeText)
}

func toBoolean(value any) (any, error) {
	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return nil, invalid(value, TypeBoolean)
}

func toIP(value any) (any, error) {
	switch typed := value.(type) {
	case netip.Addr:
		if !typed.IsValid() {
			return nil, invalid(value, TypeIP)
		}
		return typed.Unmap(), nil
	case string:
		address, err := netip.ParseAddr(strings.TrimSpace(typed))
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not an IP address", ErrValueInvalid, typed)
		}
		return address.Unmap(), nil
	}
	return nil, invalid(value, TypeIP)
}

func toMAC(value any) (any, error) {
	switch typed := value.(type) {
	case net.HardwareAddr:
		if len(typed) != 6 {
			return nil, invalid(value, TypeMAC)
		}
		return typed, nil
	case string:
		address, err := net.ParseMAC(strings.TrimSpace(typed))
		if err != nil || len(address) != 6 {
			return nil, fmt.Errorf("%w: %q is not a MAC address", ErrValueInvalid, typed)
		}
		return address, nil
	}
	return nil, invalid(value, TypeMAC)
}

// toInstant reads a time, or a number of UTC epoch SECONDS (a parse function's answer, which may
// carry a fraction), or an RFC 3339 text that carries its offset. A text with no offset is refused:
// which zone it meant is not something a column type can assume.
func toInstant(value any) (any, error) {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return nil, invalid(value, TypeInstant)
		}
		return typed.UTC(), nil
	case int64:
		return time.Unix(typed, 0).UTC(), nil
	case float64:
		if math.IsInf(typed, 0) || math.IsNaN(typed) {
			return nil, invalid(value, TypeInstant)
		}
		seconds, fraction := math.Modf(typed)
		return time.Unix(int64(seconds), int64(math.Round(fraction*1e9))).UTC(), nil
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(typed))
		if err != nil {
			return nil, fmt.Errorf("%w: %q is not an RFC 3339 instant with its offset", ErrValueInvalid, typed)
		}
		return parsed.UTC(), nil
	}
	return nil, invalid(value, TypeInstant)
}

// Role is the data catalogue role a type takes when the declaration states none (PS4): an instant
// is time, a number is a measure, anything else a dimension.
func Role(columnType string) string {
	switch columnType {
	case TypeInstant:
		return "time"
	case TypeInteger, TypeReal:
		return "measure"
	}
	return "dimension"
}
