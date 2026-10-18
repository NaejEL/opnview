package frontjs_test

import "testing"

// TestTheSourcesViewNamesAnOPNsensePageByThePluginsLabel (carried item 6 of Lot 4): the link to the OPNsense
// page that turns a provider on is named by the catalogue key its plugin declares, as /api/sources serves
// it, so no front file and no surface catalogue lists the pages.
func TestTheSourcesViewNamesAnOPNsensePageByThePluginsLabel(t *testing.T) {
	t.Parallel()
	page := newFakeServer(t, true, true).page(t, "/sources")
	want := englishCatalogue(t)["plugin.suricata.opnsense_page.ids_administration"]
	if want == "" {
		t.Fatal("the suricata plugin's catalogue names no Intrusion Detection page")
	}
	if page.Count("a.opnsense-page") != 1 {
		t.Fatalf("no OPNsense page link:\n%s", page.Outline())
	}
	if got := page.Text("a.opnsense-page"); got != want {
		t.Errorf("the OPNsense page link reads %q, want the plugin's %q", got, want)
	}
	if href := page.Attribute("a.opnsense-page", "href"); href != "https://firewall.example/ui/ids" {
		t.Errorf("the OPNsense page link points at %q", href)
	}
}
