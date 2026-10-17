package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/NaejEL/opnview/internal/config"
	"github.com/NaejEL/opnview/internal/dataset"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/widget/datasets"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// The datasets a plugin's scripts read (PA3 of the plugin-API amendment to
// specs/SPEC-backend-completion-plugins.md).
//
// A MANIFEST NAMES WHAT ITS SCRIPTS READ. `datasets` lists the data-catalogue names -- a core dataset's own
// name, another plugin's <plugin>.<table> -- that its source script, its derivations and its widgets read
// through host.datasets. Its own tables need no entry. A read of anything else is refused with
// dataset_not_declared: at load, when a script names the dataset by a literal, which this file finds by
// reading every script of the plugin's directory; at the call, when the name is computed, by the host
// (scripthost and the widget host check the same set). A declared dataset the installation lacks is no
// refusal: the read answers the named absence unknown_dataset, as it always did.

// literalDatasetRead is a host.datasets call naming its dataset by a string literal.
var literalDatasetRead = regexp.MustCompile(`host\s*\.\s*datasets\s*\.\s*([A-Za-z_]+)\s*\(\s*["']([^"']*)["']`)

// DeclaredDatasets is the set of dataset names a plugin's scripts may read: those its manifest names.
// A name under the plugin's own prefix (<plugin>.) is its own table's, and always readable.
type DeclaredDatasets struct {
	plugin string
	names  map[string]bool
}

// NewDeclaredDatasets is the set a manifest declares.
func NewDeclaredDatasets(plugin string, names []string) DeclaredDatasets {
	declared := DeclaredDatasets{plugin: plugin, names: map[string]bool{}}
	for _, name := range names {
		declared.names[name] = true
	}
	return declared
}

// Admits reports whether a script of the plugin may read a dataset.
func (d DeclaredDatasets) Admits(name string) bool {
	return d.names[name] || (d.plugin != "" && strings.HasPrefix(name, d.plugin+"."))
}

// Names are the declared names, in the manifest's order of no consequence.
func (d DeclaredDatasets) Names() []string {
	names := make([]string, 0, len(d.names))
	for name := range d.names {
		names = append(names, name)
	}
	return names
}

// checkDatasets checks a manifest's `datasets` and every literal read its scripts make: each name a
// catalogue name, listed once, and every dataset a script names by a literal declared.
func checkDatasets(manifest contract.Manifest, directory fs.FS) (DeclaredDatasets, *contract.Refusal) {
	seen := map[string]bool{}
	for _, name := range manifest.Datasets {
		if !contract.DatasetNamePattern.MatchString(name) {
			return DeclaredDatasets{}, contract.Refuse(manifest.Name, contract.RefusalManifestInvalid,
				"datasets names %q, which is not a data-catalogue name (a core dataset's name or <plugin>.<table>)", name)
		}
		if seen[name] {
			return DeclaredDatasets{}, contract.Refuse(manifest.Name, contract.RefusalManifestInvalid,
				"datasets names %q twice", name)
		}
		seen[name] = true
	}
	declared := NewDeclaredDatasets(manifest.Name, manifest.Datasets)
	var refusal *contract.Refusal
	_ = fs.WalkDir(directory, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(name, ".js") || refusal != nil {
			return nil
		}
		source, readErr := fs.ReadFile(directory, name)
		if readErr != nil {
			return nil
		}
		for _, match := range literalDatasetRead.FindAllStringSubmatch(string(source), -1) {
			if !declared.Admits(match[2]) {
				refusal = contract.Refuse(manifest.Name, contract.RefusalDatasetNotDeclared,
					"%s reads the dataset %q through host.datasets.%s, and the manifest's datasets does not name it",
					name, match[2], match[1])
				return fs.SkipAll
			}
		}
		return nil
	})
	if refusal != nil {
		return DeclaredDatasets{}, refusal
	}
	return declared, nil
}

// catalogueHolder is the data catalogue a load's scripts read: the core's datasets and every loaded
// plugin's, built at the first read, once every plugin of the load is there.
type catalogueHolder struct {
	once      sync.Once
	loaded    *Loaded
	catalogue *dataset.Catalogue
	err       error
}

// get returns the catalogue.
func (h *catalogueHolder) get() (*dataset.Catalogue, error) {
	h.once.Do(func() { h.catalogue, h.err = dataset.NewCatalogue(h.loaded.Datasets()) })
	return h.catalogue, h.err
}

// datasetAccess is how the plugin's scripts read the datasets it declares, on the store it was loaded
// with. A source script's or a derivation's read withholds nothing under aggregate_mode = no_domains: what
// it writes is stored, and the aggregate mode changes what is shown, never what is stored.
func (p *Plugin) datasetAccess(options Options) *scripthost.DatasetAccess {
	declared := p.declaredDatasets
	access := &scripthost.DatasetAccess{Admits: declared.Admits}
	unavailable := func(context.Context, int64, string, json.RawMessage) (any, string, error) {
		return nil, "", fmt.Errorf("the plugin %s was loaded with no store to read from", p.Name)
	}
	access.Latest, access.Rows = unavailable, unavailable
	if options.Store == nil || options.catalogue == nil {
		return access
	}
	database, holder := options.Store, options.catalogue
	prepare := func(ctx context.Context, now int64) (*request.Request, error) {
		catalogue, err := holder.get()
		if err != nil {
			return nil, err
		}
		read := &request.Request{Ctx: ctx, Db: database, Now: now, Datasets: catalogue, Mode: request.AggregateModeFull}
		retention, err := read.Setting(config.KeyRetentionSeconds, strconv.FormatInt(config.DefaultRetentionSeconds, 10))
		if err != nil {
			return nil, err
		}
		read.Retention, _ = strconv.ParseInt(retention, 10, 64)
		return read, nil
	}
	access.Latest = func(ctx context.Context, now int64, name string, filter json.RawMessage) (any, string, error) {
		read, err := prepare(ctx, now)
		if err != nil {
			return nil, "", err
		}
		latest, refusal, err := datasets.ReadLatestRows(read, name, filter)
		if refusal != nil {
			return nil, refusal.Code + " at " + refusal.Path, nil
		}
		return latest, "", err
	}
	access.Rows = func(ctx context.Context, now int64, name string, options json.RawMessage) (any, string, error) {
		read, err := prepare(ctx, now)
		if err != nil {
			return nil, "", err
		}
		rows, refusal, err := datasets.ReadRows(read, name, options, false)
		if refusal != nil {
			return nil, refusal.Code + " at " + refusal.Path, nil
		}
		return rows, "", err
	}
	return access
}
