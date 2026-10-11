package broker_test

import (
	"testing"

	"github.com/NaejEL/opnview/internal/broker"
)

// TestTheProductVersionIsReadFromSystemInformation reads the version out of the first entry
// systemInformationAction lists, sprintf('%s %s-%s', product_name, product_version, product_arch)
// (docs/opnsense-api-survey.md, "Firewall versions, read from source for A19").
func TestTheProductVersionIsReadFromSystemInformation(t *testing.T) {
	t.Parallel()
	for entry, want := range map[string]string{
		"OPNsense 26.7.3-amd64":           "26.7.3",
		"OPNsense 26.7.3_1-amd64":         "26.7.3_1",
		"OPNsense Business 26.10-aarch64": "26.10",
		"OPNsense":                        "",
		"OPNsense amd64":                  "",
		"":                                "",
	} {
		got, ok := broker.ProductVersion(entry)
		if got != want || ok != (want != "") {
			t.Errorf("%q reads as %q (%v), not %q", entry, got, ok, want)
		}
	}
}
