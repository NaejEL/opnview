package provider

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

// The kind files: one per core kind, kinds/<kind>.json, found by walking the embedded directory, so
// adding a core kind's contract is adding a file and no list, switch or registry in Go names it (PA2 of the
// plugin-API amendment to specs/SPEC-backend-completion-plugins.md). shared_records.json holds the
// record every kind shares: the result.
//
//go:embed kinds shared_records.json
var kindFiles embed.FS

var (
	kindsOnce sync.Once
	coreKinds []Contract
	shared    []Record
	kindsErr  error
)

func loadKinds() { coreKinds, shared, kindsErr = ReadKinds(kindFiles) }

// KindsError is what reading the embedded kind files met: nil for a sound build. The plugin loader
// reports it as a failure of the built-ins, which the suite fails on.
func KindsError() error {
	kindsOnce.Do(loadKinds)
	return kindsErr
}

// ReadKinds reads the kind files of a tree laid out as this package's: kinds/<kind>.json and
// shared_records.json. A test reads a tree of its own with it.
func ReadKinds(fsys fs.FS) ([]Contract, []Record, error) {
	raw, err := fs.ReadFile(fsys, "shared_records.json")
	if err != nil {
		return nil, nil, fmt.Errorf("provider: reading the shared records: %w", err)
	}
	var sharedFile struct {
		Records []Record `json:"records"`
	}
	if err := decodeStrictly(raw, &sharedFile); err != nil {
		return nil, nil, fmt.Errorf("provider: shared_records.json: %w", err)
	}
	entries, err := fs.ReadDir(fsys, "kinds")
	if err != nil {
		return nil, nil, fmt.Errorf("provider: reading the kind files: %w", err)
	}
	var contracts []Contract
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := fs.ReadFile(fsys, path.Join("kinds", entry.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("provider: %w", err)
		}
		var contract Contract
		if err := decodeStrictly(raw, &contract); err != nil {
			return nil, nil, fmt.Errorf("provider: kinds/%s: %w", entry.Name(), err)
		}
		if contract.Kind+".json" != entry.Name() {
			return nil, nil, fmt.Errorf("provider: kinds/%s declares the kind %q: the file is named after its kind",
				entry.Name(), contract.Kind)
		}
		if problem := checkContract(contract, sharedFile.Records); problem != "" {
			return nil, nil, fmt.Errorf("provider: kinds/%s: %s", entry.Name(), problem)
		}
		contracts = append(contracts, contract)
	}
	sort.Slice(contracts, func(i, j int) bool { return contracts[i].Kind < contracts[j].Kind })
	if problem := checkSettingsAcross(contracts); problem != "" {
		return nil, nil, fmt.Errorf("provider: the kind files: %s", problem)
	}
	return contracts, sharedFile.Records, nil
}

// decodeStrictly decodes one JSON value, refusing a key the format does not have.
func decodeStrictly(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// checkContract reports what is wrong with a kind file, empty when nothing is.
func checkContract(contract Contract, sharedRecords []Record) string {
	if !TermShapeValid(contract.Kind) || strings.TrimSpace(contract.Description) == "" {
		return "the kind needs a lower-case name and a description"
	}
	if problem := checkSettings(contract); problem != "" {
		return problem
	}
	if contract.CoreOnly != "" {
		if len(contract.Functions) > 0 || len(contract.Records) > 0 || contract.Push {
			return "a core-only kind has no contract and no push entry: no plugin implements it"
		}
		return ""
	}
	if len(contract.Functions) == 0 {
		return "a kind a plugin implements names its functions"
	}
	known := map[string]bool{}
	for _, record := range append(append([]Record(nil), sharedRecords...), contract.Records...) {
		known[record.Name] = true
	}
	if contract.Push != (contract.PushRecord != "") || (contract.PushRecord != "" && !known[contract.PushRecord]) {
		return "a kind a push entry may deliver names the record table the entry fills, and only such a kind does"
	}
	if contract.LiveAnswer != "" && !known[contract.LiveAnswer] {
		return fmt.Sprintf("live_answer names %q, which no record table names", contract.LiveAnswer)
	}
	for _, function := range contract.Functions {
		if !known[function.Returns] {
			return fmt.Sprintf("the function %s returns %q, which no record table names", function.Name, function.Returns)
		}
	}
	for _, record := range append([]Record{{Name: "values", Fields: contract.Values}}, contract.Records...) {
		for _, field := range record.Fields {
			switch field.Type {
			case FieldText, FieldInteger, FieldNumber, FieldBoolean, FieldTextList, FieldObject:
			case FieldRecord, FieldRecords:
				if !known[field.Of] {
					return fmt.Sprintf("%s.%s names the record %q, which no record table names", record.Name, field.Name,
						field.Of)
				}
			default:
				return fmt.Sprintf("%s.%s has the type %q", record.Name, field.Name, field.Type)
			}
		}
	}
	return ""
}

// Kinds returns every core kind's declaration, ordered by kind: those that accept a plugin provider
// and those only the core implements.
func Kinds() []Contract {
	kindsOnce.Do(loadKinds)
	return append([]Contract(nil), coreKinds...)
}

// KindOf returns a core kind's declaration, whether a plugin may implement it or not.
func KindOf(kind string) (Contract, bool) {
	kindsOnce.Do(loadKinds)
	for _, contract := range coreKinds {
		if contract.Kind == kind {
			return contract, true
		}
	}
	return Contract{}, false
}

// ContractOf returns the contract of a core kind a plugin may implement.
func ContractOf(kind string) (Contract, bool) {
	contract, found := KindOf(kind)
	if !found || contract.CoreOnly != "" {
		return Contract{}, false
	}
	return contract, true
}

// CoreOnly reports whether a kind is a core kind no plugin may implement, and why.
func CoreOnly(kind string) (string, bool) {
	contract, found := KindOf(kind)
	if !found || contract.CoreOnly == "" {
		return "", false
	}
	return contract.CoreOnly, true
}

// CoreKinds returns every core kind's name, sorted. A provider set adds the kinds its plugins
// declared (Set.Kinds).
func CoreKinds() []string {
	var kinds []string
	for _, contract := range Kinds() {
		kinds = append(kinds, contract.Kind)
	}
	return kinds
}

// IsCoreKind reports whether a kind is a core kind, implementable by a plugin or not.
func IsCoreKind(kind string) bool {
	_, found := KindOf(kind)
	return found
}

// IsServiceKind reports whether a kind is a service kind: an external service's data a plugin
// acquires and answers from, rather than a firewall source.
func IsServiceKind(kind string) bool {
	contract, found := KindOf(kind)
	return found && contract.Service
}

// TermShapeValid reports whether a term a provider declares -- a measure, a unit, a subject
// kind, a set key -- has the shape the schema constrains: non-empty, lower-case, no space.
func TermShapeValid(term string) bool {
	return term != "" && term == strings.ToLower(term) && !strings.Contains(term, " ")
}

// SharedRecords are the record tables every kind shares: the result.
func SharedRecords() []Record {
	kindsOnce.Do(loadKinds)
	return append([]Record(nil), shared...)
}

// LookupRecord returns a record table a contract names: its own, or a shared one.
func LookupRecord(contract Contract, name string) (Record, bool) {
	if record, found := contract.Record(name); found {
		return record, true
	}
	for _, record := range SharedRecords() {
		if record.Name == name {
			return record, true
		}
	}
	return Record{}, false
}

// FunctionOf returns one of a contract's functions by name.
func (c Contract) FunctionOf(name string) (Function, bool) {
	for _, function := range c.Functions {
		if function.Name == name {
			return function, true
		}
	}
	return Function{}, false
}
