package ipfixregistry

import "testing"

// TestTheRegistryIsReadWhole: the embedded registry parses, and the elements the decoder relies on
// carry the registry's names and abstract types.
func TestTheRegistryIsReadWhole(t *testing.T) {
	t.Parallel()
	if err := Err(); err != nil {
		t.Fatal(err)
	}
	if Len() < 400 {
		t.Errorf("the registry holds %d elements", Len())
	}
	for id, want := range map[uint16][2]string{1: {"octetDeltaCount", TypeUnsigned64},
		8: {"sourceIPv4Address", TypeIPv4Address}, 27: {"sourceIPv6Address", TypeIPv6Address},
		152: {"flowStartMilliseconds", TypeDateTimeMilliseconds}, 56: {"sourceMacAddress", TypeMACAddress}} {
		element, found := Lookup(id)
		if !found || element.Name != want[0] || element.AbstractType != want[1] {
			t.Errorf("element %d is %+v", id, element)
		}
		if byName, found := ByName(want[0]); !found || byName.ID != id {
			t.Errorf("%s is %+v", want[0], byName)
		}
	}
}
