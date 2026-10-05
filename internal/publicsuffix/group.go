package publicsuffix

import (
	"sort"

	"github.com/NaejEL/opnview/internal/store"
)

// GroupingState says whether site names can be grouped by registrable domain.
type GroupingState string

// The two states of the grouping.
const (
	// GroupingAvailable means a copy of the list is in use.
	GroupingAvailable GroupingState = "available"
	// GroupingUnavailable means no copy has ever been downloaded, so no grouping is
	// offered at all: a name is never grouped by a guess at where its suffix ends.
	GroupingUnavailable GroupingState = "unavailable"
)

// DomainGroup is the site names that share one registrable domain, with their
// figures summed.
type DomainGroup struct {
	// RegistrableDomain is the group's registrable domain, or the site name itself
	// when the name has none -- a name that is itself a public suffix, or one the
	// algorithm refuses -- in which case Registrable is false.
	RegistrableDomain string
	Registrable       bool
	// SiteNames are the stored names in the group, verbatim and unaltered.
	SiteNames   []string
	Bytes       int64
	Connections int64
}

// Group collapses site totals into their registrable domains, or reports that no
// grouping is available. Distinct clients are not summed across names: a client
// that reached two names of one domain would be counted twice, so a group carries
// no client count, and the caller counts clients per name.
func (r *Refresher) Group(sites []store.SiteTotal) ([]DomainGroup, GroupingState) {
	list := r.List()
	if list == nil {
		return nil, GroupingUnavailable
	}
	return GroupSites(list, sites), GroupingAvailable
}

// GroupSites collapses site totals with one copy of the list.
func GroupSites(list *List, sites []store.SiteTotal) []DomainGroup {
	groups := map[string]*DomainGroup{}
	for _, site := range sites {
		domain, registrable := list.RegistrableDomain(site.SiteName)
		if !registrable {
			domain = site.SiteName
		}
		key := domain
		if !registrable {
			// A name with no registrable domain is a group of its own, kept apart from a
			// registrable domain that happens to be spelled the same.
			key = "\x00" + site.SiteName
		}
		group, present := groups[key]
		if !present {
			group = &DomainGroup{RegistrableDomain: domain, Registrable: registrable}
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
