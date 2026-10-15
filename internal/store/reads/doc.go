// Package reads is the part of the store holding the read paths, on the read-only connections:
// volume windows and series, the connection tree, attribution rates and totals (read.go), the
// surfaces' own rows (surfaces.go), widget and catalogue statements (widgetrows.go,
// catalogue.go), and what a query reads (queryreads.go). internal/store composes it into Store.
package reads
