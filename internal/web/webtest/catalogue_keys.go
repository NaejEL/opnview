package webtest

import (
	"fmt"
	"io/fs"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/plugin"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/web"
	"github.com/NaejEL/opnview/internal/web/messages"
)

// What a test asks of the catalogue and the product never does: the catalogue every linked surface and
// every built-in plugin make, the catalogue files themselves, and every key the Go code can choose. They
// live here, in the test-support package, rather than in the packages the binary links (CL-AC3 of
// cleanup-amendment.md, restated by PA-AC24).

// ProductCatalogue returns the catalogue of every surface linked into this binary and of every built-in
// plugin, together with the core's: what a server built with the registered surfaces serves. A test uses
// it to name the string a reader sees without building a server.
func ProductCatalogue(language string) (web.Catalogue, error) {
	merged, err := web.LoadCatalogue(language)
	if err != nil {
		return web.Catalogue{}, err
	}
	combined := make(map[string]string, len(merged.Strings))
	for key, value := range merged.Strings {
		combined[key] = value
	}
	for _, surface := range web.RegisteredSurfaces() {
		localised, err := web.SurfaceStrings(surface.Files)
		if err != nil {
			return web.Catalogue{}, fmt.Errorf("webtest: the surface %q: %s", surface.Name, err)
		}
		for key, value := range localised[language] {
			combined[key] = value
		}
	}
	for key, value := range web.WithDatasetStrings(language, plugin.BuiltinsLoaded().Strings(language)) {
		if _, taken := combined[key]; !taken {
			combined[key] = value
		}
	}
	merged.Strings = combined
	return merged, nil
}

// CatalogueSources returns the text of every catalogue file of the core and of every registered surface,
// by a path that names its owner. A test reads them to assert what no language file may contain.
func CatalogueSources() (map[string]string, error) {
	sources := map[string]string{}
	entries, err := web.CoreLocales.ReadDir("locale")
	if err != nil {
		return nil, fmt.Errorf("webtest: listing the core's catalogue files: %w", err)
	}
	for _, entry := range entries {
		raw, err := web.CoreLocales.ReadFile("locale/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("webtest: reading %s: %w", entry.Name(), err)
		}
		sources["locale/"+entry.Name()] = string(raw)
	}
	for _, surface := range web.RegisteredSurfaces() {
		if surface.Files == nil {
			continue
		}
		localeEntries, err := fs.ReadDir(surface.Files, "locale")
		if err != nil {
			continue
		}
		for _, entry := range localeEntries {
			raw, err := fs.ReadFile(surface.Files, "locale/"+entry.Name())
			if err != nil {
				return nil, fmt.Errorf("webtest: reading %s of %s: %w", entry.Name(), surface.Name, err)
			}
			sources["surfaces/"+surface.Name+"/locale/"+entry.Name()] = string(raw)
		}
	}
	return sources, nil
}

// AllHandlerKeys is every catalogue key the Go code of the core and of every registered surface can
// choose, and every key a built-in plugin carries -- reached through the registry rows and the settings
// its manifest declares, which the loader confines its keys to -- so a test can assert that each exists.
func AllHandlerKeys() []web.MessageKey {
	keys := CoreKeys()
	for _, surface := range web.RegisteredSurfaces() {
		if surface.Keys != nil {
			keys = append(keys, surface.Keys()...)
		}
	}
	for key := range plugin.BuiltinsLoaded().Strings(web.SourceLanguage) {
		keys = append(keys, web.MessageKey(key))
	}
	core, _ := dataset.Core()
	for _, declaration := range core {
		keys = append(keys, web.MessageKey(declaration.Label))
	}
	return keys
}

// KindLabelKey names a provider kind, as the front composes it from a registry row.
func KindLabelKey(kind string) messages.Key { return messages.Key("collection.kind." + kind) }

// ProviderLabelKey names one registry row: the same product under two kinds is two entries.
func ProviderLabelKey(kind, providerKey string) messages.Key {
	return messages.Key("provider." + kind + "." + providerKey)
}

// GapReasonKey names one value of collection_gap.reason.
func GapReasonKey(reason string) messages.Key { return messages.Key("gap_reason." + reason) }

// CoreKeys is every key the core's Go code chooses (internal/web/messages) with the registry's vocabulary:
// the labels the collection and sources views name a source with, one key per core kind and per provider
// its file registers, and one per gap reason the declarations name. NO KIND, PROVIDER OR REASON IS NAMED
// HERE: each is composed from the kind files and the declared gap reasons, so adding a kind or a protocol
// is adding its file and its catalogue entry.
func CoreKeys() []messages.Key {
	keys := []messages.Key{
		messages.MsgSetupClosed, messages.MsgSessionExpired, messages.MsgFormTokenRefused, messages.MsgKeyFileRefused,
		messages.MsgInternal, messages.MsgUnitDay, messages.MsgUnitDays, messages.MsgUnitHour, messages.MsgUnitHours,
		messages.MsgUnitMinute, messages.MsgUnitMinutes, messages.MsgUnitSecond, messages.MsgUnitSeconds,
		messages.MsgDurationUnitUnknown, messages.MsgCollectionIntervalTooLarge, messages.MsgCollectionValueInvalid,
		messages.MsgAvailabilityHealthy, messages.MsgAvailabilityNotCollecting, messages.MsgAvailabilityUnavailable,
		messages.MsgAvailabilityNotYetProbed, messages.MsgAvailabilityUnknown,
		"collection.label.gap_reason", "collection.group.implementations", "state.sizing.page_not_adjustable",
		"collection.label.retention_now", "collection.label.retention_requested", "notice.edited_not_saved",
		"state.secret_not_kept", "sources.label.opnsense_page",
	}
	kinds, _ := provider.CoreDeclarations()
	for _, contract := range provider.Kinds() {
		keys = append(keys, KindLabelKey(contract.Kind))
		for _, registered := range contract.Registered {
			keys = append(keys, ProviderLabelKey(contract.Kind, registered.Key))
		}
	}
	for _, declared := range store.GapReasons(kinds) {
		keys = append(keys, GapReasonKey(string(declared.Reason)))
	}
	return keys
}
