package ipfixregistry

import (
	"bufio"
	_ "embed"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

//go:embed elements.csv
var elementsFile string

// Element is one Information Element of the registry.
type Element struct {
	// ID is the element id; for an IANA element the enterprise number is zero.
	ID uint16
	// Name is the registry's name.
	Name string
	// AbstractType is the registry's abstract data type (RFC 7012, section 3.1): unsigned64,
	// ipv4Address, dateTimeMilliseconds, ...
	AbstractType string
	// Semantics is the registry's data type semantics, or empty.
	Semantics string
}

// The abstract data types of RFC 7012, section 3.1, as the registry writes them.
const (
	TypeOctetArray           = "octetArray"
	TypeUnsigned8            = "unsigned8"
	TypeUnsigned16           = "unsigned16"
	TypeUnsigned32           = "unsigned32"
	TypeUnsigned64           = "unsigned64"
	TypeSigned8              = "signed8"
	TypeSigned16             = "signed16"
	TypeSigned32             = "signed32"
	TypeSigned64             = "signed64"
	TypeFloat32              = "float32"
	TypeFloat64              = "float64"
	TypeBoolean              = "boolean"
	TypeMACAddress           = "macAddress"
	TypeString               = "string"
	TypeDateTimeSeconds      = "dateTimeSeconds"
	TypeDateTimeMilliseconds = "dateTimeMilliseconds"
	TypeDateTimeMicroseconds = "dateTimeMicroseconds"
	TypeDateTimeNanoseconds  = "dateTimeNanoseconds"
	TypeIPv4Address          = "ipv4Address"
	TypeIPv6Address          = "ipv6Address"
)

var (
	loadOnce sync.Once
	byID     map[uint16]Element
	byName   map[string]Element
	loadErr  error
)

func load() {
	byID, byName = map[uint16]Element{}, map[string]Element{}
	scanner := bufio.NewScanner(strings.NewReader(elementsFile))
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Split(text, ",")
		if len(fields) != 4 {
			loadErr = fmt.Errorf("ipfixregistry: elements.csv line %d has %d fields, not 4", line, len(fields))
			return
		}
		id, err := strconv.ParseUint(fields[0], 10, 16)
		if err != nil {
			loadErr = fmt.Errorf("ipfixregistry: elements.csv line %d: %w", line, err)
			return
		}
		element := Element{ID: uint16(id), Name: fields[1], AbstractType: fields[2], Semantics: fields[3]}
		byID[element.ID] = element
		byName[element.Name] = element
	}
}

// Lookup returns the IANA element with an id.
func Lookup(id uint16) (Element, bool) {
	loadOnce.Do(load)
	element, found := byID[id]
	return element, found
}

// ByName returns the IANA element with a name.
func ByName(name string) (Element, bool) {
	loadOnce.Do(load)
	element, found := byName[name]
	return element, found
}

// Len is how many elements the registry holds.
func Len() int {
	loadOnce.Do(load)
	return len(byID)
}

// Err is the error reading the embedded registry met, or nil.
func Err() error {
	loadOnce.Do(load)
	return loadErr
}

// EnterpriseName is the name a field of an enterprise-specific element is exposed under, which no
// registry names: its enterprise number and its element id, so two vendors' fields never collide
// (PS2 of the push amendment).
func EnterpriseName(enterprise uint32, id uint16) string {
	return fmt.Sprintf("enterprise_%d_%d", enterprise, id)
}
