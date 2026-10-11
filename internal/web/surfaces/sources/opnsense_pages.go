package sources

import "strings"

// The OPNsense pages that turn an installed provider on.
//
// docs/widget-catalogue.md, Source availability: present_but_disabled reads "installed,
// not collecting" WITH THE OPNSENSE PAGE THAT TURNS IT ON, and Part 3 of the same
// document adds that the page is always given with the statement that opnview never
// performs that change. The Sources surface draws the link beside the state; the label
// heading it says that the change is made in OPNsense and never by opnview.
//
// EVERY PATH IS OPNSENSE'S OWN, cited from the menu definitions of opnsense/core at the
// 26.7.3 tag the API survey is written for (docs/opnsense-api-survey.md, Pinned
// version), and each is the page holding the setting the collector found off. No path
// is guessed. The host is never written here: the link is the firewall base URL the
// operator entered in the settings, followed by the path, and with no URL entered no
// link is drawn. Following a link is the reader's own act in the reader's own browser;
// drawing it contacts nothing.
//
// A provider absent from this table gets no link, and that is declared rather than
// filled in:
//   - dns_lookup/dnsmasq, resolver_cache/dnsmasq and resolver_local_data/dnsmasq: no
//     OPNsense setting makes them collect -- their reads are unsupported by opnview
//     (internal/collect), so a page would promise something it cannot deliver;
//   - firewall_log/pf: its collector never records present_but_disabled;
//   - geo_asn and public_suffix: not OPNsense's at all;

// opnsensePage is one page of the OPNsense web interface.
type opnsensePage struct {
	// Path is the page's path on the firewall, as the menu definition gives it.
	Path string
	// LabelKey names the page by its OPNsense menu entry.
	LabelKey messageKey
}

// The pages, each cited from its menu definition at opnsense/core 26.7.3.
var (
	// Services > Intrusion Detection > Administration, which holds ids.general.enabled
	// (controllers/OPNsense/IDS/forms/generalSettings.xml).
	// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/IDS/Menu/Menu.xml
	pageIDSAdministration = opnsensePage{Path: "/ui/ids", LabelKey: msgPageIDSAdministration}
	// Services > Kea DHCP > Kea DHCPv4, which holds dhcpv4.general.enabled
	// (controllers/OPNsense/Kea/forms/generalSettings4.xml, drawn by DhcpController::v4Action).
	// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Kea/Menu/Menu.xml
	pageKeaDHCPv4 = opnsensePage{Path: "/ui/kea/dhcp/v4", LabelKey: msgPageKeaDHCPv4}
	// Services > Dnsmasq DNS & DHCP > DHCP ranges, where dnsmasq.dhcp_ranges is filled.
	// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Dnsmasq/Menu/Menu.xml
	pageDnsmasqDHCPRanges = opnsensePage{Path: "/ui/dnsmasq/settings#dhcpranges",
		LabelKey: msgPageDnsmasqDHCPRanges}
	// Services > Unbound DNS > General, which holds unbound.general.enabled
	// (controllers/OPNsense/Unbound/forms/general.xml).
	// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Unbound/Menu/Menu.xml
	pageUnboundGeneral = opnsensePage{Path: "/ui/unbound/general", LabelKey: msgPageUnboundGeneral}
	// Reporting > Unbound DNS, whose page draws the dnsreporting form holding
	// unbound.general.stats -- the flag /api/unbound/overview/is_enabled reports
	// (controllers/OPNsense/Unbound/OverviewController.php, forms/dnsreporting.xml).
	// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Core/Menu/Menu.xml
	pageUnboundReporting = opnsensePage{Path: "/ui/unbound/overview", LabelKey: msgPageUnboundReporting}
	// Reporting > NetFlow, which holds netflow.collect.enable, the "local" flag
	// /api/diagnostics/netflow/is_enabled reports (controllers/OPNsense/Diagnostics/
	// forms/netflow_capture.xml).
	// https://github.com/opnsense/core/blob/26.7.3/src/opnsense/mvc/app/models/OPNsense/Diagnostics/Menu/Menu.xml
	pageNetFlow = opnsensePage{Path: "/ui/diagnostics/netflow", LabelKey: msgPageNetFlow}
)

// opnsensePagesFor are the pages that turn one installed provider on, by the kind and
// the provider key the registry row carries. Unbound's lookups are off for one of two
// settings, on two pages, and the row does not say which without reading its detail,
// which is never parsed: both pages are given.
func opnsensePagesFor(kind, providerKey string) []opnsensePage {
	switch kind + "/" + providerKey {
	case "security_event/suricata":
		return []opnsensePage{pageIDSAdministration}
	case "dhcp_lease/kea":
		return []opnsensePage{pageKeaDHCPv4}
	case "dhcp_lease/dnsmasq":
		return []opnsensePage{pageDnsmasqDHCPRanges}
	case "dns_lookup/unbound":
		return []opnsensePage{pageUnboundGeneral, pageUnboundReporting}
	case "resolver_cache/unbound", "resolver_local_data/unbound":
		return []opnsensePage{pageUnboundGeneral}
	case "flow_volume/insight", "measurement_sample/insight":
		return []opnsensePage{pageNetFlow}
	}
	return nil
}

// opnsenseLink is one page as a link: its address on the configured firewall and its
// name.
type opnsenseLink struct {
	URL      string
	LabelKey messageKey
}

// opnsenseLinks are the links to the pages that turn a provider on, on the firewall at
// baseURL, or none when no firewall URL is configured.
func opnsenseLinks(baseURL, kind, providerKey string) []opnsenseLink {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil
	}
	var links []opnsenseLink
	for _, page := range opnsensePagesFor(kind, providerKey) {
		links = append(links, opnsenseLink{URL: baseURL + page.Path, LabelKey: page.LabelKey})
	}
	return links
}
