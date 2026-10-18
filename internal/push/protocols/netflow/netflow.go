package netflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/push/ipfixregistry"
)

func init() { push.Register(protocol{}) }

// Name is the protocol's name in a push declaration.
const Name = "netflow"

// ReasonRequiredMissing counts a record lacking an element a subscriber requires: skipped for that
// subscriber.
const ReasonRequiredMissing = "netflow_required_element_missing"

type protocol struct{}

func (protocol) Name() string                               { return Name }
func (protocol) Transports() []string                       { return []string{push.TransportUDP} }
func (protocol) NewDecoder(limits push.Limits) push.Decoder { return newDecoder(limits) }
func (protocol) NewFramer(io.Reader, int) push.Framer       { return nil }
func (protocol) Fields() map[string]string                  { return nil }

// FieldType is the plugin type of a registry element, by name; the elements the decoder adds from
// the header are registry elements too.
func (protocol) FieldType(name string) (string, bool) {
	element, found := ipfixregistry.ByName(name)
	if !found {
		return "", false
	}
	return columnTypeOf(element.AbstractType), true
}

// LossDetectable: NetFlow v9 and IPFIX carry a sequence number per exporter and domain (PS7).
func (protocol) LossDetectable(string) bool { return true }

// Filter is a NetFlow subscription's filter: the Information Elements the subscriber requires, by
// registry name, and optional conditions on decoded fields, with the data catalogue's closed
// operator list (P8).
type Filter struct {
	Require []string    `json:"require"`
	Where   []Condition `json:"where,omitempty"`
}

// Condition is one condition on a decoded field.
type Condition struct {
	Element  string `json:"element"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
	Values   []any  `json:"values,omitempty"`
}

// The operators, the data catalogue's (internal/dataset, query.go).
const (
	operatorEq        = "eq"
	operatorNe        = "ne"
	operatorIn        = "in"
	operatorNotIn     = "not_in"
	operatorLt        = "lt"
	operatorLe        = "le"
	operatorGt        = "gt"
	operatorGe        = "ge"
	operatorIsNull    = "is_null"
	operatorIsNotNull = "is_not_null"
)

func (protocol) Compile(raw json.RawMessage) (push.Selector, error) {
	var filter Filter
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&filter); err != nil {
		return nil, fmt.Errorf("the netflow filter is not {require, where}: %w", err)
	}
	if len(filter.Require) == 0 {
		return nil, fmt.Errorf("the netflow filter requires no element: a subscriber names the elements its rows " +
			"are built from")
	}
	compiled := &selector{}
	for _, name := range filter.Require {
		if err := known(name); err != nil {
			return nil, err
		}
		compiled.require = append(compiled.require, name)
	}
	for _, condition := range filter.Where {
		if err := known(condition.Element); err != nil {
			return nil, err
		}
		columnType, _ := protocol{}.FieldType(condition.Element)
		check, err := compileCondition(condition, columnType)
		if err != nil {
			return nil, err
		}
		compiled.where = append(compiled.where, check)
	}
	return compiled, nil
}

// known checks an element name against the registry. An enterprise field's name is accepted as
// enterprise_<number>_<id>, the name the decoder gives it.
func known(name string) error {
	if _, found := ipfixregistry.ByName(name); found {
		return nil
	}
	var enterprise, id uint64
	if n, _ := fmt.Sscanf(name, "enterprise_%d_%d", &enterprise, &id); n == 2 &&
		name == ipfixregistry.EnterpriseName(uint32(enterprise), uint16(id)) {
		return nil
	}
	return fmt.Errorf("%q is not an Information Element of the IANA registry", name)
}

type selector struct {
	require []string
	where   []func(fields map[string]any) bool
}

func (s *selector) Keys() []string { return nil }

func (s *selector) Admits(record *push.Record) (bool, string) {
	for _, name := range s.require {
		if _, present := record.Fields[name]; !present {
			return false, ReasonRequiredMissing
		}
	}
	for _, check := range s.where {
		if !check(record.Fields) {
			return false, ""
		}
	}
	return true, ""
}

// compileCondition builds one condition, converting its operands to the element's type once.
func compileCondition(condition Condition, columnType string) (func(map[string]any) bool, error) {
	operand := func(value any) (any, error) {
		if number, ok := value.(json.Number); ok {
			if integer, err := number.Int64(); err == nil {
				value = integer
			} else if real, err := number.Float64(); err == nil {
				value = real
			}
		}
		converted, err := push.Convert(columnType, value)
		if err != nil {
			return nil, fmt.Errorf("the condition on %s: %w", condition.Element, err)
		}
		return converted, nil
	}
	name := condition.Element
	switch condition.Operator {
	case operatorIsNull:
		return func(fields map[string]any) bool { _, present := fields[name]; return !present }, nil
	case operatorIsNotNull:
		return func(fields map[string]any) bool { _, present := fields[name]; return present }, nil
	case operatorIn, operatorNotIn:
		if len(condition.Values) == 0 {
			return nil, fmt.Errorf("the condition on %s names no value", name)
		}
		var values []any
		for _, value := range condition.Values {
			converted, err := operand(value)
			if err != nil {
				return nil, err
			}
			values = append(values, converted)
		}
		member := condition.Operator == operatorIn
		return func(fields map[string]any) bool {
			value, ok := fieldValue(fields, name, columnType)
			if !ok {
				return !member
			}
			for _, candidate := range values {
				if compare(value, candidate) == 0 {
					return member
				}
			}
			return !member
		}, nil
	case operatorEq, operatorNe, operatorLt, operatorLe, operatorGt, operatorGe:
		converted, err := operand(condition.Value)
		if err != nil {
			return nil, err
		}
		if converted == nil {
			return nil, fmt.Errorf("the condition on %s names no value", name)
		}
		operator := condition.Operator
		return func(fields map[string]any) bool {
			value, ok := fieldValue(fields, name, columnType)
			if !ok {
				return operator == operatorNe
			}
			result := compare(value, converted)
			if result == incomparable {
				return operator == operatorNe
			}
			switch operator {
			case operatorEq:
				return result == 0
			case operatorNe:
				return result != 0
			case operatorLt:
				return result < 0
			case operatorLe:
				return result <= 0
			case operatorGt:
				return result > 0
			}
			return result >= 0
		}, nil
	}
	return nil, fmt.Errorf("the operator %q is not one of the data catalogue's", condition.Operator)
}

// fieldValue converts a decoded field to its column type for a comparison.
func fieldValue(fields map[string]any, name, columnType string) (any, bool) {
	raw, present := fields[name]
	if !present {
		return nil, false
	}
	converted, err := push.Convert(columnType, raw)
	if err != nil || converted == nil {
		return nil, false
	}
	return converted, true
}

// incomparable is what compare answers for two values of different types.
const incomparable = 2

// compare orders two values of one column type: -1, 0 or 1, or incomparable.
func compare(a, b any) int {
	switch x := a.(type) {
	case int64:
		if y, ok := b.(int64); ok {
			return order(x < y, x > y)
		}
	case float64:
		if y, ok := b.(float64); ok {
			return order(x < y, x > y)
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	case bool:
		if y, ok := b.(bool); ok && x == y {
			return 0
		}
	case netip.Addr:
		if y, ok := b.(netip.Addr); ok {
			return x.Compare(y)
		}
	case net.HardwareAddr:
		if y, ok := b.(net.HardwareAddr); ok {
			return bytes.Compare(x, y)
		}
	case time.Time:
		if y, ok := b.(time.Time); ok {
			return x.Compare(y)
		}
	}
	return incomparable
}

func order(less, greater bool) int {
	switch {
	case less:
		return -1
	case greater:
		return 1
	}
	return 0
}
