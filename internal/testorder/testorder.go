package testorder

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

// StartFirst reorders m's tests so the named ones come first, in the order given, and every
// other test keeps its source order behind them. It must be called from TestMain, before m.Run.
// It returns an error, and reorders nothing, when testing.M no longer holds its tests where this
// package expects them, when a name is given twice, or when a name is not a test of the package.
func StartFirst(m *testing.M, names ...string) error {
	if m == nil {
		return fmt.Errorf("testorder: no testing.M")
	}
	field := reflect.ValueOf(m).Elem().FieldByName("tests")
	if !field.IsValid() {
		return fmt.Errorf("testorder: testing.M holds no field named tests in this Go release")
	}
	if field.Type() != reflect.TypeFor[[]testing.InternalTest]() {
		return fmt.Errorf("testorder: testing.M's field tests is a %s, not a []testing.InternalTest", field.Type())
	}
	// The field is unexported, so reflection reads it but refuses to write it; its address, taken
	// from the addressable value and converted in the same expression, is what lets the slice's
	// elements be swapped in place.
	tests := (*[]testing.InternalTest)(unsafe.Pointer(field.UnsafeAddr()))
	ordered, err := order(*tests, names)
	if err != nil {
		return err
	}
	copy(*tests, ordered)
	return nil
}

// order returns tests with the named ones first, in the order given, and the others behind them in
// their own order.
func order(tests []testing.InternalTest, names []string) ([]testing.InternalTest, error) {
	position := make(map[string]int, len(tests))
	for index, test := range tests {
		position[test.Name] = index
	}
	first := make(map[string]bool, len(names))
	var unknown []string
	for _, name := range names {
		if first[name] {
			return nil, fmt.Errorf("testorder: %s is named twice", name)
		}
		if _, present := position[name]; !present {
			unknown = append(unknown, name)
		}
		first[name] = true
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("testorder: not a test of this package: %s", strings.Join(unknown, ", "))
	}
	ordered := make([]testing.InternalTest, 0, len(tests))
	for _, name := range names {
		ordered = append(ordered, tests[position[name]])
	}
	for _, test := range tests {
		if !first[test.Name] {
			ordered = append(ordered, test)
		}
	}
	return ordered, nil
}
