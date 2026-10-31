package figures

import (
	"sort"

	"github.com/NaejEL/opnview/internal/widget/request"
)

// The site names over a window and the rows they sum to are the figures library's sites area since
// ADR 0021 (plugins/figures); what stays is the registrable-domain grouping the services area reads.

// RegistrableGrouping maps each site name to its registrable domain, or reports that no
// list has been downloaded yet.
func RegistrableGrouping(r *request.Request, lists request.SuffixLists, names []string) (map[string]string, bool) {
	if lists == nil {
		return nil, false
	}
	domains, available := lists.RegistrableDomains(r.Ctx, names)
	if !available {
		return nil, false
	}
	// A name with no registrable domain -- one that is itself a public suffix -- stands for itself.
	result := make(map[string]string, len(names))
	for _, name := range names {
		if domain, found := domains[name]; found {
			result[name] = domain
		} else {
			result[name] = name
		}
	}
	return result, true
}

// SortedKeys returns a map's keys in order.
func SortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
