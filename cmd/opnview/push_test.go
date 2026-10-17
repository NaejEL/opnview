package main

import (
	"flag"
	"net/netip"
	"testing"
)

// TestPushExtraSendersAreReadFromTheCommandLine is open question 10 (b): --push-extra-senders takes a
// comma-separated list, repeatable, an empty value -- the unset environment variable docker compose
// passes through -- names none, an IPv4-mapped address is read as the IPv4 one, and anything that is
// not an IP address is refused at start. The addresses are generated from the documentation ranges,
// so no network's address is written here.
func TestPushExtraSendersAreReadFromTheCommandLine(t *testing.T) {
	t.Parallel()
	parse := func(arguments ...string) (pushExtraSenders, error) {
		var senders pushExtraSenders
		flags := flag.NewFlagSet("opnview", flag.ContinueOnError)
		flags.SetOutput(discard{})
		flags.Var(&senders, "push-extra-senders", "")
		return senders, flags.Parse(arguments)
	}
	gateway := netip.AddrFrom4([4]byte{198, 51, 100, 1})
	other := netip.AddrFrom4([4]byte{203, 0, 113, 9})
	documentation := netip.MustParsePrefix("2001:db8::/32").Addr().Next()
	mapped := netip.AddrFrom16(other.As16())

	none, err := parse()
	if err != nil || len(none) != 0 {
		t.Errorf("by default: %v, %v", none, err)
	}
	empty, err := parse("--push-extra-senders", "")
	if err != nil || len(empty) != 0 {
		t.Errorf("an empty value: %v, %v", empty, err)
	}
	several, err := parse("--push-extra-senders", gateway.String()+", "+documentation.String(),
		"--push-extra-senders", mapped.String())
	want := gateway.String() + "," + documentation.String() + "," + other.String()
	if err != nil || several.String() != want {
		t.Errorf("several: %q, %v, want %q", several.String(), err, want)
	}
	if _, err := parse("--push-extra-senders", netip.PrefixFrom(gateway, 24).String()); err == nil {
		t.Error("a prefix was accepted as an address")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
