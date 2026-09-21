package buildinfo

import "testing"

func TestSummaryKeepsAStampedVersion(t *testing.T) {
	got := Summary("1.2.3")
	want := "opnview 1.2.3"
	if got != want {
		t.Fatalf("Summary(%q) = %q, want %q", "1.2.3", got, want)
	}
}

func TestSummaryFallsBackToDevelOnABlankVersion(t *testing.T) {
	for _, version := range []string{"", " ", "\t\n"} {
		got := Summary(version)
		want := AppName + " " + DevelVersion
		if got != want {
			t.Errorf("Summary(%q) = %q, want %q", version, got, want)
		}
	}
}
