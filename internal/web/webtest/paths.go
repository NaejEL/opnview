package webtest

import (
	"net/url"
	"strconv"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/web"
)

// The paths and form-field names the tests drive.
//
// THEY ARE WRITTEN HERE AGAIN, deliberately, rather than imported from the surfaces that serve
// them: a test that read its protocol from the code under test could not notice the code
// changing it. A surface's own paths are its package's constants; these are what a browser
// would be told.
const (
	// PathCollection is the collection surface.
	PathCollection = "/collection"
	// PathDatasets is the data catalogue's listing.
	PathDatasets = "/api/datasets"
	// PathCollectionSettings is where a settings card of the collection surface is saved.
	PathCollectionSettings = "/collection/settings"
	// PathInterfaces is the Interfaces surface.
	PathInterfaces = "/interfaces"
	// PathSources is the Sources surface.
	PathSources = "/sources"
)

// The form fields and card names of the surfaces the tests drive, as a browser would submit
// them.
const (
	// The collection surface's sized kinds' cards.
	FieldKind     = "kind"
	FieldPageSize = "page_size"
	FieldInterval = "interval_seconds"
	FieldFill     = "fill_suggestion"

	// The settings surface's cards.
	FieldSettingsCard        = "settings_card"
	SettingsCardFirewall     = "firewall"
	SettingsCardGeolocation  = "geolocation"
	SettingsCardPublicSuffix = "public_suffix"
	SettingsCardAppearance   = "appearance"

	// The Interfaces surface's forms.
	FieldNetworkAction = "action"
	FieldInterfaceID   = "interface"
	FieldNetwork       = "network"
	FieldLinkLocal     = "link_local"
)

// CardFieldName is one collection card's field: the card's id, a dot, the field.
func CardFieldName(card, field string) string { return card + "." + field }

// SavedLocation is where the Interfaces surface sends the browser after a saved edit to one
// interface: the surface, naming the card, at that card's anchor.
func SavedLocation(interfaceID int64) string {
	id := strconv.FormatInt(interfaceID, 10)
	return PathInterfaces + "?" + url.Values{"saved": {id}}.Encode() + "#interface_" + id
}

// YesNo names a stored boolean as the surfaces draw it.
func YesNo(value bool) MessageKey {
	if value {
		return "state.yes"
	}
	return "state.no"
}

// MessageKey is a catalogue key.
type MessageKey = web.MessageKey

// The actions a form of the Interfaces surface asks for, and the two values of its link-local
// rule, as the forms submit them.
const (
	ActionAddNetwork     = "add"
	ActionConfirmNetwork = "confirm"
	ActionRemoveNetwork  = "remove"
	ActionRestoreNetwork = "restore"
	ActionLinkLocal      = "link_local"
	LinkLocalYes         = "yes"
	LinkLocalNo          = "no"

	// FieldCard names the settings card a form of the collection surface is about.
	FieldCard = "card"
)

// More form fields of the collection surface's settings cards, and the site-name modes.
const (
	FieldConfirm         = "confirm"
	FieldCancel          = "cancel"
	FieldSelectionPrefix = "selection_"

	// FieldPublicSuffixInterval is the Public Suffix List refresh interval's field: the
	// setting's own key.
	FieldPublicSuffixInterval = config.KeyPublicSuffixRefreshInterval

	// The two values of the site-name mode.
	AggregateModeFull      = "full"
	AggregateModeNoDomains = "no_domains"
)
