package sources

import "strings"

// The links to the OPNsense pages that turn an installed provider on. Each provider declares its pages in
// its plugin's manifest, a core kind's registered row in its kind file (plugin.Loaded.EnablePages); this
// surface only joins a page's path to the firewall base URL the operator entered, and with no URL entered
// draws no link. Following a link is the reader's own act in the reader's own browser; drawing it contacts
// nothing. The label heading the links says the change is made in OPNsense and never by opnview.

// opnsenseLink is one page as a link: its address on the configured firewall and its name.
type opnsenseLink struct {
	URL      string
	LabelKey messageKey
}

// opnsenseLinks are the links to the pages that turn a provider on, on the firewall at baseURL, or none when
// no firewall URL is configured.
func (s *surface) opnsenseLinks(baseURL, kind, providerKey string) []opnsenseLink {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		return nil
	}
	var links []opnsenseLink
	for _, page := range s.Plugins().EnablePages(kind, providerKey) {
		links = append(links, opnsenseLink{URL: baseURL + page.Path, LabelKey: messageKey(page.Label)})
	}
	return links
}
