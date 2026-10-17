package webtest

import (
	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
)

// The routes and body members the tests drive.
//
// THEY ARE WRITTEN HERE AGAIN, deliberately, rather than imported from the surfaces that serve
// them: a test that read its protocol from the code under test could not notice the code
// changing it. A surface's own paths are its package's constants; these are what the front is
// told.
const (
	// PathAPISession is the session state and the form token.
	PathAPISession = "/api/session"
	// PathAPISetup creates the first account.
	PathAPISetup = "/api/account/setup"
	// PathAPISignIn issues a session.
	PathAPISignIn = "/api/session/sign-in"
	// PathAPISettings is every figure of the settings.
	PathAPISettings = "/api/settings"
	// PathAPICollection is every figure of the collection.
	PathAPICollection = "/api/collection"
	// PathAPICollectionKind saves one kind's edit: append the kind.
	PathAPICollectionKind = "/api/collection/kinds/"
	// PathAPICollectionSettings saves one group of settings: append the card.
	PathAPICollectionSettings = "/api/collection/settings/"
	// PathAPIInterfaceNetworks is every interface, and an edit of one.
	PathAPIInterfaceNetworks = "/api/interface-networks"
	// PathAPISources is every provider, outage and recent gap.
	PathAPISources = "/api/sources"
	// PathDatasets is the data catalogue's listing.
	PathDatasets = "/api/datasets"
)

// ReviewFirewallURL is a firewall address a test stores where one must be configured: an RFC 2606
// name, which no real firewall answers.
const ReviewFirewallURL = "https://firewall.example.net"

// MessageKey is a catalogue key.
type MessageKey = web.MessageKey

// The actions an edit of the Interfaces surface asks for.
const (
	ActionAddNetwork     = "add"
	ActionConfirmNetwork = "confirm"
	ActionRemoveNetwork  = "remove"
	ActionRestoreNetwork = "restore"
	ActionLinkLocal      = "link_local"
)

// The setting keys the settings saves carry, and the site-name modes.
const (
	// FieldPublicSuffixInterval is the Public Suffix List refresh interval's setting key.
	FieldPublicSuffixInterval = config.KeyPublicSuffixRefreshInterval

	// The two values of the site-name mode.
	AggregateModeFull      = "full"
	AggregateModeNoDomains = "no_domains"
)
