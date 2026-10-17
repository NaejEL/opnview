package plugin

import (
	"strings"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
)

// The OPNsense pages that turn an installed provider on (docs/widget-catalogue.md, Source availability:
// present_but_disabled reads "installed, not collecting" WITH THE OPNSENSE PAGE THAT TURNS IT ON, and Part
// 3 adds that the page is always given with the statement that opnview never performs that change).
//
// EACH PROVIDER DECLARES ITS OWN, in its manifest (contract.EnablePage): a path on the firewall, the
// catalogue key naming the page, and the menu definition at the OPNsense version the survey pins that
// establishes the path -- a built-in carries it, which a test holds to the pinned version; a third party is
// asked to, as for an endpoint's upstream_url. No path is guessed. The host is never written: the Sources surface links the
// firewall base URL the operator entered followed by the path, and with no URL entered draws no link. A
// provider declaring none gets no link, which is how a provider no OPNsense setting makes collect says so.

// checkEnablePages validates the pages a manifest's providers declare.
func checkEnablePages(manifest contract.Manifest) *contract.Refusal {
	namespace := "plugin." + manifest.Name + "."
	for _, declaration := range manifest.Providers {
		for _, page := range declaration.EnablePages {
			switch {
			case !strings.HasPrefix(page.Path, "/ui/") || strings.ContainsAny(page.Path, " \"<>"):
				return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "the %s provider's enable page %q is "+
					"not a path of the firewall's web interface, /ui/...", declaration.Kind, page.Path)
			case !strings.HasPrefix(page.Label, namespace):
				return contract.Refuse(manifest.Name, contract.RefusalManifestInvalid, "the %s provider's enable page %s names "+
					"its label by a key of the plugin's own catalogue, %s*", declaration.Kind, page.Path, namespace)
			}
		}
	}
	return nil
}

// EnablePages returns the OPNsense pages that turn one registry row's provider on, as its plugin declares
// them. None when nothing declares one: a core kind's registered row (flow_volume's) has no implementation,
// so nothing ever finds it installed and not collecting.
func (l *Loaded) EnablePages(kind, providerKey string) []provider.EnablePage {
	if l != nil {
		for _, member := range l.Members {
			for _, info := range member.Providers {
				if info.Kind != kind || info.Key != providerKey {
					continue
				}
				for _, declaration := range member.Manifest.Providers {
					key := declaration.Key
					if key == "" {
						key = member.Name
					}
					if declaration.Kind == kind && key == providerKey {
						return append([]provider.EnablePage(nil), declaration.EnablePages...)
					}
				}
			}
		}
	}
	return nil
}
