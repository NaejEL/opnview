package params

import (
	"sort"

	"github.com/NaejEL/opnview/internal/collect/provider"
)

// The provider kinds a manifest may bind (`providers`, `primary`) are data, not a list here (PA2 of the
// plugin-API amendment to specs/SPEC-backend-completion-plugins.md): each core kind's file says whether it
// is `bindable`, and a plugin's own kind is bindable unless its manifest says otherwise. A provider with
// no availability row reads state_unknown, never healthy; availability and activeness are two separate
// fields.

// CoreBindableKinds are the core kinds whose files make them bindable, sorted.
func CoreBindableKinds() []string {
	var kinds []string
	for _, kind := range provider.Kinds() {
		if kind.Bindable {
			kinds = append(kinds, kind.Kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}
