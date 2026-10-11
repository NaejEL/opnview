package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/push"
	// Every push protocol registers itself: a declaration is checked against the protocols linked in.
	_ "github.com/NaejEL/opnview/internal/push/protocols"
	"github.com/NaejEL/opnview/internal/store"
)

// A plugin's push sources (PS3 of the push amendment, docs/plugins.md "Push sources"): the data the
// firewall SENDS to opnview -- syslog lines, NetFlow records -- which the user configures on the
// firewall, and which opnview receives, decodes once per message and routes to every plugin that
// subscribes. The core holds the listeners and the generic decoders (internal/push); what a
// program's message says, and which columns a NetFlow record fills, is the plugin's, declared here.

// PushDeclaration is one push entry of a manifest.
type PushDeclaration struct {
	// Name is the entry's own name, a snake_case word: the parse function's name under the script's
	// push namespace, and the catalogue's.
	Name string `json:"name"`
	// Protocol is a registered push protocol: syslog or netflow.
	Protocol string `json:"protocol"`
	// Filter is the header filter, in the protocol's own form: {program, facility, severity} for
	// syslog, {require, where} for NetFlow.
	Filter json.RawMessage `json:"filter"`
	// Transports are the transports the entry accepts; empty is every one its protocol is received
	// over. An entry feeding a core kind is accepted only over the transports whose loss is detectable,
	// whatever it says -- TCP for syslog, UDP for NetFlow (Q9 of the push amendment).
	Transports []string `json:"transports,omitempty"`
	// Pattern is a regular expression in RE2 syntax whose named groups name output columns: the
	// fast path for a syslog message a regular expression can express.
	Pattern string `json:"pattern,omitempty"`
	// Parse names a function the script exports under `push`, called with a batch of decoded
	// messages, for a syslog message a regular expression cannot express.
	Parse string `json:"parse,omitempty"`
	// Fields maps output columns -- or, for an entry naming a kind, the fields of the kind's record -- to
	// fields every record carries: a syslog header field, or a NetFlow Information Element by registry
	// name -- one name, or a list whose first present one wins.
	Fields map[string]PushField `json:"fields,omitempty"`
	// OutputTable names one of the plugin's tables; Kind a core kind the plugin provides. One of the
	// two, never both.
	OutputTable string `json:"output_table,omitempty"`
	Kind        string `json:"kind,omitempty"`
	// Replaces names one of the plugin's endpoints, whose polling the entry replaces while it is alive.
	Replaces string `json:"replaces,omitempty"`
	// LivenessSeconds is how long the entry stays alive with no message; zero is push.DefaultLiveness.
	LivenessSeconds int `json:"liveness_seconds,omitempty"`
	// SurveySection names the section of docs/opnsense-api-survey.md establishing what the firewall
	// sends; a built-in carries it.
	SurveySection string `json:"survey_section,omitempty"`
}

// PushField is where one column's value comes from: field names, the first present one wins.
type PushField []string

// UnmarshalJSON reads one name or a list of names.
func (f *PushField) UnmarshalJSON(raw []byte) error {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		*f = PushField{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return fmt.Errorf("a push field mapping is a field name or a list of field names")
	}
	*f = many
	return nil
}

// pushBinding is one push entry, checked and compiled.
type pushBinding struct {
	plugin      string
	declaration PushDeclaration
	protocol    push.Protocol
	selector    push.Selector
	pattern     *regexp.Regexp
	table       store.PluginTable
	transports  []string
	liveness    time.Duration
	// providerKind and providerKey are the provider the entry's availability and gaps belong to.
	providerKind, providerKey string
}

// ID is the entry's subscription identity: <plugin>.<entry>.
func (b *pushBinding) ID() string { return b.plugin + "." + b.declaration.Name }

// pushParseNamespace is the namespace a script exports its parse functions under.
const pushParseNamespace = "push"

// checkPush checks every push entry of a manifest against its protocol, its tables, its providers
// and its script's exports.
func checkPush(plugin *Plugin, exports map[string]map[string]string) ([]*pushBinding, *Refusal) {
	manifest := plugin.manifest
	if len(manifest.Push) == 0 {
		return nil, nil
	}
	tables := map[string]store.PluginTable{}
	for _, table := range plugin.tables {
		tables[table.Table] = table
	}
	var pushProviders []ProviderDeclaration
	providedKinds := map[string]ProviderDeclaration{}
	for _, declaration := range manifest.Providers {
		if declaration.Source == providerSourcePush {
			pushProviders = append(pushProviders, declaration)
		}
		providedKinds[declaration.Kind] = declaration
	}
	seen := map[string]bool{}
	var bindings []*pushBinding
	for _, declaration := range manifest.Push {
		binding, refusal := checkPushEntry(plugin, declaration, tables, pushProviders, providedKinds, exports)
		if refusal != nil {
			return nil, refusal
		}
		if seen[declaration.Name] {
			return nil, refuse(plugin.Name, RefusalPushDeclarationInvalid, "the push entry %q is declared twice",
				declaration.Name)
		}
		seen[declaration.Name] = true
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func checkPushEntry(plugin *Plugin, declaration PushDeclaration, tables map[string]store.PluginTable,
	pushProviders []ProviderDeclaration, providedKinds map[string]ProviderDeclaration,
	exports map[string]map[string]string) (*pushBinding, *Refusal) {
	name := plugin.Name
	if !namePattern.MatchString(declaration.Name) {
		return nil, refuse(name, RefusalPushDeclarationInvalid, "the push entry name %q is not a snake_case word",
			declaration.Name)
	}
	entry := fmt.Sprintf("the push entry %q", declaration.Name)
	protocol, known := push.Lookup(declaration.Protocol)
	if !known {
		return nil, refuse(name, RefusalPushProtocolUnknown, "%s names the protocol %q; this build decodes %v",
			entry, declaration.Protocol, push.Protocols())
	}
	binding := &pushBinding{plugin: name, declaration: declaration, protocol: protocol}
	if len(bytes.TrimSpace(declaration.Filter)) == 0 {
		return nil, refuse(name, RefusalPushFilterInvalid, "%s declares no filter", entry)
	}
	selector, err := protocol.Compile(declaration.Filter)
	if err != nil {
		code := RefusalPushFilterInvalid
		if isUnknownElement(err) {
			code = RefusalPushElementUnknown
		}
		return nil, refuse(name, code, "%s: %v", entry, err)
	}
	binding.selector = selector

	offered := map[string]bool{}
	for _, transport := range protocol.Transports() {
		offered[transport] = true
	}
	for _, transport := range declaration.Transports {
		if !offered[transport] {
			return nil, refuse(name, RefusalPushDeclarationInvalid, "%s accepts %s, which %s is not received over",
				entry, transport, declaration.Protocol)
		}
	}
	binding.transports = append([]string(nil), declaration.Transports...)
	if len(binding.transports) == 0 {
		binding.transports = protocol.Transports()
	}

	binding.liveness = push.DefaultLiveness
	if declaration.LivenessSeconds != 0 {
		binding.liveness = time.Duration(declaration.LivenessSeconds) * time.Second
		if binding.liveness < push.LivenessFloor {
			return nil, refuse(name, RefusalPushLivenessBelowFloor, "%s declares liveness_seconds %d, below the "+
				"floor of %d", entry, declaration.LivenessSeconds, int(push.LivenessFloor/time.Second))
		}
	}

	// How a message becomes a row.
	fieldsOf := protocol.Fields()
	for column, sources := range declaration.Fields {
		if len(sources) == 0 {
			return nil, refuse(name, RefusalPushDeclarationInvalid, "%s maps the column %q to no field", entry, column)
		}
		for _, source := range sources {
			if _, known := protocol.FieldType(source); !known {
				return nil, refuse(name, RefusalPushElementUnknown, "%s maps the column %q to %q, which %s does "+
					"not carry", entry, column, source, declaration.Protocol)
			}
		}
	}
	if declaration.Parse != "" {
		if fieldsOf == nil {
			return nil, refuse(name, RefusalPushDeclarationInvalid, "%s declares a parse function: a %s record "+
				"is mapped to columns by its fields, with no script", entry, declaration.Protocol)
		}
		if exports[pushParseNamespace][declaration.Parse] != "function" {
			return nil, refuse(name, RefusalPushParseMissing, "%s names the parse function %s.%s, which the "+
				"script does not export", entry, pushParseNamespace, declaration.Parse)
		}
	}
	if declaration.Pattern != "" {
		if fieldsOf == nil {
			return nil, refuse(name, RefusalPushDeclarationInvalid, "%s declares a pattern: a %s record carries "+
				"no message text", entry, declaration.Protocol)
		}
		if declaration.Parse != "" {
			return nil, refuse(name, RefusalPushDeclarationInvalid, "%s declares both a pattern and a parse "+
				"function", entry)
		}
		pattern, err := regexp.Compile(declaration.Pattern)
		if err != nil {
			return nil, refuse(name, RefusalPushPatternInvalid, "%s: the pattern does not compile under RE2: %v",
				entry, err)
		}
		binding.pattern = pattern
	}

	// Where rows go.
	switch {
	case declaration.OutputTable != "" && declaration.Kind != "",
		declaration.OutputTable == "" && declaration.Kind == "":
		return nil, refuse(name, RefusalPushDestinationInvalid, "%s names %s: one of output_table and kind, "+
			"never both", entry, describeDestination(declaration))
	case declaration.OutputTable != "":
		if refusal := checkPushTable(plugin, binding, tables, pushProviders, entry); refusal != nil {
			return nil, refusal
		}
	default:
		if refusal := checkPushKind(plugin, binding, providedKinds, entry); refusal != nil {
			return nil, refusal
		}
	}
	if declaration.Replaces != "" {
		if _, declared := plugin.endpoints[declaration.Replaces]; !declared {
			return nil, refuse(name, RefusalPushReplacesUnknown, "%s replaces %q, which the manifest does not "+
				"declare as an endpoint", entry, declaration.Replaces)
		}
	}
	return binding, nil
}

func describeDestination(declaration PushDeclaration) string {
	if declaration.OutputTable == "" && declaration.Kind == "" {
		return "neither"
	}
	return "both"
}

// isUnknownElement reports a filter refused for naming a field its protocol does not carry.
func isUnknownElement(err error) bool {
	return bytes.Contains([]byte(err.Error()), []byte("is not an Information Element"))
}

// providerSourcePush is ProviderDeclaration.Source for a provider its push entries feed.
const providerSourcePush = "push"

// checkPushTable checks an entry writing into one of the plugin's own tables.
func checkPushTable(plugin *Plugin, binding *pushBinding, tables map[string]store.PluginTable,
	pushProviders []ProviderDeclaration, entry string) *Refusal {
	name := plugin.Name
	declaration := binding.declaration
	table, declared := tables[declaration.OutputTable]
	if !declared {
		return refuse(name, RefusalPushDestinationInvalid, "%s writes into %q, which the plugin does not declare",
			entry, declaration.OutputTable)
	}
	timeColumn, _ := tableColumn(table, table.TimeColumn)
	if table.TimeColumn == "" || timeColumn.Type != push.TypeInstant {
		return refuse(name, RefusalPushTableWithoutInstant, "%s writes into %s, whose time column is not an "+
			"instant: a push output table is a hypertable chunked on an instant", entry, table.Table)
	}
	if len(pushProviders) != 1 {
		return refuse(name, RefusalPushProviderMissing, "%s writes into a table of the plugin's own, which needs "+
			"exactly one provider of a kind of the plugin's own with source push; the plugin declares %d",
			entry, len(pushProviders))
	}
	binding.table = table
	key := pushProviders[0].Key
	if key == "" {
		key = plugin.Name
	}
	binding.providerKind, binding.providerKey = pushProviders[0].Kind, key
	if binding.pattern != nil {
		for _, group := range binding.pattern.SubexpNames() {
			if group == "" {
				continue
			}
			if _, isColumn := tableColumn(table, group); !isColumn {
				return refuse(name, RefusalPushPatternInvalid, "%s: the named group %q is not a column of %s",
					entry, group, table.Table)
			}
		}
	}
	for column := range declaration.Fields {
		if _, isColumn := tableColumn(table, column); !isColumn {
			return refuse(name, RefusalPushDeclarationInvalid, "%s maps %q, which is not a column of %s", entry,
				column, table.Table)
		}
	}
	if declaration.Parse != "" {
		// What the parse function returns is checked per row, against the table.
		return nil
	}
	filled := map[string]bool{table.TimeColumn: true}
	if binding.pattern != nil {
		for _, group := range binding.pattern.SubexpNames() {
			filled[group] = true
		}
	}
	for column := range declaration.Fields {
		filled[column] = true
	}
	for _, column := range table.Columns {
		if !column.Nullable && !filled[column.Name] {
			return refuse(name, RefusalPushColumnUnfilled, "%s leaves the NOT NULL column %s of %s unfilled", entry,
				column.Name, table.Table)
		}
	}
	return nil
}

// checkPushKind checks an entry delivering records of a core kind, which the same plugin provides.
func checkPushKind(plugin *Plugin, binding *pushBinding, providedKinds map[string]ProviderDeclaration,
	entry string) *Refusal {
	name := plugin.Name
	declaration := binding.declaration
	provided, declared := providedKinds[declaration.Kind]
	if !declared || !provider.IsCoreKind(declaration.Kind) {
		return refuse(name, RefusalPushDestinationInvalid, "%s delivers records of %q, which is not a core kind the "+
			"plugin provides", entry, declaration.Kind)
	}
	table := declaration.Kind + "_record"
	contract, _ := provider.ContractOf(declaration.Kind)
	record, hasTable := provider.LookupRecord(contract, table)
	switch {
	case !hasTable:
		return refuse(name, RefusalPushDestinationInvalid, "%s delivers records of %s, whose contract has no %s "+
			"table a push entry could fill", entry, declaration.Kind, table)
	case declaration.Parse == "" && len(declaration.Fields) == 0:
		return refuse(name, RefusalPushDeclarationInvalid, "%s delivers records of %s, which a parse function "+
			"builds or a field mapping fills: the records are the kind's contract", entry, declaration.Kind)
	case declaration.Parse != "" && len(declaration.Fields) > 0:
		return refuse(name, RefusalPushDeclarationInvalid, "%s delivers records of %s with both a parse function "+
			"and a field mapping: one of the two builds them", entry, declaration.Kind)
	case declaration.Pattern != "":
		return refuse(name, RefusalPushDeclarationInvalid, "%s delivers records of %s with a pattern: a record of a "+
			"kind is built by a parse function or a field mapping", entry, declaration.Kind)
	}
	if len(declaration.Fields) > 0 {
		// A mapping fills contract fields, and every required one.
		for column := range declaration.Fields {
			if _, known := record.Field(column); !known {
				return refuse(name, RefusalPushDeclarationInvalid, "%s maps %q, which is not a field of %s", entry,
					column, table)
			}
		}
		for _, field := range record.Fields {
			if _, mapped := declaration.Fields[field.Name]; field.Required && !mapped {
				return refuse(name, RefusalPushColumnUnfilled, "%s leaves the required field %s of %s unmapped",
					entry, field.Name, table)
			}
		}
	}
	if _, adapted := pushKinds[declaration.Kind]; !adapted {
		return refuse(name, RefusalPushDestinationInvalid, "%s delivers records of %s, which no push path of the "+
			"core receives yet", entry, declaration.Kind)
	}
	// PS9, as the NetFlow join amendment's PS17 amends it. The load check tells the two cases apart by
	// the CONTRACT, never by the plugin: an entry delivers the record table <kind>_record, and the
	// kind's polled functions can return the tables PolledRecordTables lists. When that list holds the
	// entry's table, the provider delivers the same records by both paths, and the entry must name the
	// endpoint it replaces, or the two paths would count them twice (push_kind_without_replacement).
	// When it does not -- flow_record's polled function returns the capture configuration, a different
	// record -- the entry is push-only, and a `replaces` would replace nothing.
	polled := provider.PolledRecordTables(contract)[table]
	switch {
	case polled && declaration.Replaces == "":
		return refuse(name, RefusalPushKindWithoutReplacement, "%s delivers %s records beside a provider that polls "+
			"them, and declares no replaces: without an exclusive source per instant the two paths would count "+
			"records twice", entry, declaration.Kind)
	case !polled && declaration.Replaces != "":
		return refuse(name, RefusalPushDeclarationInvalid, "%s replaces %q, but no polled function of %s delivers %s "+
			"records: there is nothing to replace", entry, declaration.Replaces, declaration.Kind, table)
	}
	key := provided.Key
	if key == "" {
		key = plugin.Name
	}
	binding.providerKind, binding.providerKey = declaration.Kind, key
	// A core kind is fed only over a transport whose loss is detectable (Q9): TCP for syslog, whose UDP
	// loss is invisible; UDP for NetFlow, whose sequence numbers detect loss.
	var detectable []string
	for _, transport := range binding.protocol.Transports() {
		if binding.protocol.LossDetectable(transport) {
			detectable = append(detectable, transport)
		}
	}
	if len(detectable) == 0 {
		return refuse(name, RefusalPushDeclarationInvalid, "%s delivers records of %s over no transport whose loss "+
			"is detectable", entry, declaration.Kind)
	}
	binding.transports = detectable
	return nil
}

// tableColumn returns a table's column by name.
func tableColumn(table store.PluginTable, name string) (store.PluginColumn, bool) {
	for _, column := range table.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return store.PluginColumn{}, false
}

// PushEntry is one push declaration as the plugin listing reports it.
type PushEntry struct {
	Name          string   `json:"name"`
	Protocol      string   `json:"protocol"`
	Transports    []string `json:"transports"`
	OutputTable   string   `json:"output_table,omitempty"`
	Kind          string   `json:"kind,omitempty"`
	Replaces      string   `json:"replaces,omitempty"`
	Columns       []string `json:"columns"`
	LivenessSecs  int64    `json:"liveness_seconds"`
	ProviderKind  string   `json:"provider_kind"`
	ProviderKey   string   `json:"provider_key"`
	SurveySection string   `json:"survey_section,omitempty"`
	// ListenerState and Alive are the receiver's view, once it runs: listening or
	// listener_not_configured, and the entry's liveness.
	ListenerState string `json:"listener_state,omitempty"`
	Alive         *bool  `json:"alive,omitempty"`
}

// listing is what the plugin listing reports of an entry.
func (b *pushBinding) listing() PushEntry {
	entry := PushEntry{Name: b.declaration.Name, Protocol: b.declaration.Protocol,
		Transports: append([]string{}, b.transports...), OutputTable: b.declaration.OutputTable,
		Kind: b.declaration.Kind, Replaces: b.declaration.Replaces, Columns: b.columns(),
		LivenessSecs: int64(b.liveness / time.Second), ProviderKind: b.providerKind, ProviderKey: b.providerKey,
		SurveySection: b.declaration.SurveySection}
	return entry
}

// columns are the columns the entry captures: its table's, or its kind's record fields.
func (b *pushBinding) columns() []string {
	var columns []string
	if b.table.Table != "" {
		for _, column := range b.table.Columns {
			columns = append(columns, column.Name)
		}
		return columns
	}
	if contract, core := provider.ContractOf(b.declaration.Kind); core {
		if record, found := provider.LookupRecord(contract, b.declaration.Kind+"_record"); found {
			for _, field := range record.Fields {
				columns = append(columns, field.Name)
			}
		}
	}
	sort.Strings(columns)
	if columns == nil {
		columns = []string{}
	}
	return columns
}

// lossDetectable says whether a loss between the firewall and opnview is detectable for every
// transport the entry accepts.
func (b *pushBinding) lossDetectable() bool {
	for _, transport := range b.transports {
		if !b.protocol.LossDetectable(transport) {
			return false
		}
	}
	return true
}

// listedPush is the plugin's push entries with the receiver's view of each.
func (p *Plugin) listedPush() []PushEntry {
	entries := p.PushEntries()
	var hub *push.Hub
	if p.pushHost != nil {
		hub = p.pushHost.hub.Load()
	}
	if hub == nil {
		return entries
	}
	for index := range entries {
		if state, found := hub.SubscriptionState(p.Name + "." + entries[index].Name); found {
			alive := state.Liveness.Alive
			entries[index].ListenerState, entries[index].Alive = state.ListenerState, &alive
		}
	}
	return entries
}
