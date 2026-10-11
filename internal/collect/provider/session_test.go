package provider

import (
	"reflect"
	"testing"

	"github.com/NaejEL/opnview/internal/opnsense"
)

// The port's own tests: what an implementation is granted, and how an answer is named.

func TestThePortGrantsExactlyTheEnumeratedCapabilities(t *testing.T) {
	granted := map[string]bool{}
	port := reflect.TypeOf((*Session)(nil)).Elem()
	for index := 0; index < port.NumMethod(); index++ {
		granted[port.Method(index).Name] = true
	}
	for _, capability := range []string{"Call", "ServiceState", "ReadObject", "ReadCollection", "Normaliser", "Now", "Stream"} {
		if !granted[capability] {
			t.Errorf("the port no longer grants %q, which an implementation needs", capability)
		}
		delete(granted, capability)
	}
	for surplus := range granted {
		t.Errorf("the port grants %q, which is not in the enumerated boundary", surplus)
	}
}

func TestAnUnknownOutcomeIsRequestNotMade(t *testing.T) {
	if got := ResponseOutcome(opnsense.Response{}); got == "" {
		t.Error("an empty response yielded no outcome code")
	}
	if QuoteForDetail("") != "an empty status" || QuoteForDetail("x") != "a status of x" {
		t.Error("the quoting changed")
	}
}
