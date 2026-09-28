package opnsense

import (
	"net/url"
	"strconv"
)

// The grid-search envelope, and the one helper that builds a request for it.
//
// Every OPNsense search helper answers with {"total": …, "rowCount": …,
// "current": …, "rows": [ … ]}, `total` being the count after filtering. Survey,
// "Pagination envelope". There is deliberately no Go type for the envelope
// itself: each collector decodes `rows` into its own shape, and a shared row
// type across five unrelated responses would be a fiction. What is shared is the
// REQUEST, which is this.

// Pagination returns the form values of one page of a grid search. current is
// 1-based; a rowCount of -1 means every row.
func Pagination(current, rowCount int) url.Values {
	return url.Values{
		"current":  []string{strconv.Itoa(current)},
		"rowCount": []string{strconv.Itoa(rowCount)},
	}
}
