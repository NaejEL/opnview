package provider

// Manifest is the three facts a provider's registry row is made of: its kind, its key within the
// kind, and what it is called.
//
// A provider declares them in its plugin's manifest (docs/plugins.md): the kind it implements,
// and a key that is the plugin's name unless the declaration names its own. The collector creates
// the registry row from them at the first probe round (store.EnsureProvider), so adding a provider
// edits no schema file and no list.
type Manifest struct {
	Kind        string
	Key         string
	DisplayName string
}
