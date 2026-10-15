// Command opnview is the single entry point: flags, wiring, signals, exit codes.
//
// WHAT THIS BINARY DOES NOW. It opens the database, applies the schema, creates the
// key file beside it if there is none, starts the collection loops and serves
// opnview's own interface. The firewall URL, the API key and secret, the MaxMind
// licence key and the theme are entered THROUGH THAT INTERFACE and through nothing
// else: there is no environment variable, no configuration file and no `configure`
// subcommand, which the maintainer ruled out precisely so that the settings surface
// has one place.
//
// So a first run prints a one-time setup token, and until somebody uses it to create
// the first account there is no way to configure a firewall and every collector
// reports that it has no firewall to talk to. That is the intended behaviour and not
// a defect.
//
// WHAT IT STILL CANNOT DO, STATED SO NOBODY LOOKS FOR IT. There is no canvas, no
// widget and no dashboard: the interface is a handful of surfaces — setup, sign-in,
// settings, collection, interfaces and sources — and the widget endpoints are step
// 5, the canvases step 7. The MaxMind GeoLite2 databases are downloaded with the account ID and the
// licence key entered on the settings surface, kept beside the database, and used to
// place the addresses the flows carry; the map that shows them is step 7. The Public
// Suffix List is downloaded from publicsuffix.org once a day, conditionally, to group
// inferred site names by registrable domain: the third and last outbound call.
// Classification, site-name attribution and the aggregate refresh run after each
// collection pass; the widget endpoints that read them are step 5B.
package main
