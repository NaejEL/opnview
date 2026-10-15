// Package webtest is what the web surfaces' tests share: a Harness serving opnview's handler over
// a fresh database, with a fake firewall (harness.go); sessions and pages (session.go, html.go,
// catalogue.go); JSON requests (json.go); the paths and field names the tests write out
// (paths.go); and a scan of the database (dbscan.go). The helpers of one surface live in that
// surface's own package below this one: accounttest, collectiontest, interfacestest,
// settingstest and sourcestest.
package webtest
