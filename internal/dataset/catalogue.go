package dataset

import (
	"fmt"
	"sort"
)

// Catalogue is every dataset an installation exposes: the core's, every loaded plugin's, and the
// datasets learned at run time -- the broker's mirror tables and stream recordings (A15 of the
// broker amendment), which a dynamic source lists each time the catalogue is read.
type Catalogue struct {
	byName  map[string]Declaration
	names   []string
	dynamic func() []Declaration
}

// NewCatalogue builds the catalogue of the core's datasets and the given plugin datasets. A name
// held twice is an error: a plugin's datasets are named <plugin>.<table>, so only a defect reaches
// it.
func NewCatalogue(plugins []Declaration) (*Catalogue, error) {
	core, err := Core()
	if err != nil {
		return nil, err
	}
	catalogue := &Catalogue{byName: map[string]Declaration{}}
	for _, declaration := range append(core, plugins...) {
		if _, taken := catalogue.byName[declaration.Name]; taken {
			return nil, fmt.Errorf("dataset: the name %q is declared twice", declaration.Name)
		}
		catalogue.byName[declaration.Name] = declaration
		catalogue.names = append(catalogue.names, declaration.Name)
	}
	sort.Strings(catalogue.names)
	return catalogue, nil
}

// SetDynamic gives the catalogue a source of datasets learned at run time. A dynamic dataset never
// replaces a declared one of the same name.
func (c *Catalogue) SetDynamic(source func() []Declaration) {
	if c != nil {
		c.dynamic = source
	}
}

// dynamicDeclarations lists the run-time datasets.
func (c *Catalogue) dynamicDeclarations() []Declaration {
	if c == nil || c.dynamic == nil {
		return nil
	}
	return c.dynamic()
}

// Lookup returns a dataset by name.
func (c *Catalogue) Lookup(name string) (Declaration, bool) {
	if c == nil {
		return Declaration{}, false
	}
	if declaration, found := c.byName[name]; found {
		return declaration, true
	}
	for _, declaration := range c.dynamicDeclarations() {
		if declaration.Name == name {
			return declaration, true
		}
	}
	return Declaration{}, false
}

// Declarations returns every dataset, ordered by name.
func (c *Catalogue) Declarations() []Declaration {
	if c == nil {
		return nil
	}
	result := make([]Declaration, 0, len(c.names))
	for _, name := range c.names {
		result = append(result, c.byName[name])
	}
	for _, declaration := range c.dynamicDeclarations() {
		if _, taken := c.byName[declaration.Name]; !taken {
			result = append(result, declaration)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
