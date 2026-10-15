package suffixlist

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store/reads"
)

// DomainGroup is the site names that share one registrable domain, with their figures summed.
type DomainGroup struct {
	// RegistrableDomain is the group's registrable domain, or the site name itself when the name
	// has none -- a name that is itself a public suffix, or one the algorithm refuses -- in which
	// case Registrable is false.
	RegistrableDomain string
	Registrable       bool
	// SiteNames are the stored names in the group, verbatim and unaltered.
	SiteNames   []string
	Bytes       int64
	Connections int64
}

// GroupSites collapses site totals into their registrable domains, as a registrable-domain
// function names them. Distinct clients are not summed across names: a client that reached two
// names of one domain would be counted twice, so a group carries no client count, and the caller
// counts clients per name.
func GroupSites(registrable func(name string) (string, bool), sites []reads.SiteTotal) []DomainGroup {
	groups := map[string]*DomainGroup{}
	for _, site := range sites {
		domain, isRegistrable := registrable(site.SiteName)
		if !isRegistrable {
			domain = site.SiteName
		}
		key := domain
		if !isRegistrable {
			// A name with no registrable domain is a group of its own, kept apart from a
			// registrable domain that happens to be spelled the same.
			key = "\x00" + site.SiteName
		}
		group, present := groups[key]
		if !present {
			group = &DomainGroup{RegistrableDomain: domain, Registrable: isRegistrable}
			groups[key] = group
		}
		group.SiteNames = append(group.SiteNames, site.SiteName)
		group.Bytes += site.Bytes
		group.Connections += site.Connections
	}
	result := make([]DomainGroup, 0, len(groups))
	for _, group := range groups {
		sort.Strings(group.SiteNames)
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Bytes != result[j].Bytes {
			return result[i].Bytes > result[j].Bytes
		}
		return result[i].RegistrableDomain < result[j].RegistrableDomain
	})
	return result
}
