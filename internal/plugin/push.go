package plugin

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store/plugindecl"

	// Every push protocol registers itself: a declaration is checked against the protocols linked in.
	_ "github.com/NaejEL/opnview/internal/push/protocols"
)

// A plugin's push sources (PS3 of the push amendment, docs/plugins.md "Push sources"): the data the
// firewall SENDS to opnview -- syslog lines, NetFlow records -- which the user configures on the
// firewall, and which opnview receives, decodes once per message and routes to every plugin that
// subscribes. The core holds the listeners and the generic decoders (internal/push); what a
// program's message says, and which columns a NetFlow record fills, is the plugin's, declared here.

// pushBinding is one push entry, checked and compiled.
type pushBinding struct {
	plugin      string
	declaration contract.PushDeclaration
	protocol    push.Protocol
	selector    push.Selector
	pattern     *regexp.Regexp
	table       plugindecl.PluginTable
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
func checkPush(plugin *Plugin, exports map[string]map[string]string) ([]*pushBinding, *contract.Refusal) {
	manifest := plugin.Manifest
	if len(manifest.Push) == 0 {
		return nil, nil
	}
	tables := map[string]plugindecl.PluginTable{}
	for _, table := range plugin.OwnTables {
		tables[table.Table] = table
	}
	// The providers a push entry writing a table of the plugin's own may belong to: one fed by push alone
	// (source push), or one of a kind of the plugin's own declared push that its script also polls -- the
	// netflow plugin's flow_record provider reads the exporter's configuration and receives its records.
	var pushProviders []contract.ProviderDeclaration
	providedKinds := map[string]contract.ProviderDeclaration{}
	for _, declaration := range manifest.Providers {
		_, core := provider.ContractOf(declaration.Kind)
		if declaration.Source == providerSourcePush || (!core && ownKindAttributes(plugin, declaration.Kind).Push) {
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
			return nil, contract.Refuse(plugin.Name, contract.RefusalPushDeclarationInvalid, "the push entry %q is declared twice",
				declaration.Name)
		}
		seen[declaration.Name] = true
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func checkPushEntry(plugin *Plugin, declaration contract.PushDeclaration, tables map[string]plugindecl.PluginTable,
	pushProviders []contract.ProviderDeclaration, providedKinds map[string]contract.ProviderDeclaration,
	exports map[string]map[string]string) (*pushBinding, *contract.Refusal) {
	name := plugin.Name
	if !contract.NamePattern.MatchString(declaration.Name) {
		return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "the push entry name %q is not a snake_case word",
			declaration.Name)
	}
	entry := fmt.Sprintf("the push entry %q", declaration.Name)
	protocol, known := push.Lookup(declaration.Protocol)
	if !known {
		return nil, contract.Refuse(name, contract.RefusalPushProtocolUnknown, "%s names the protocol %q; this build decodes %v",
			entry, declaration.Protocol, push.Protocols())
	}
	binding := &pushBinding{plugin: name, declaration: declaration, protocol: protocol}
	if len(bytes.TrimSpace(declaration.Filter)) == 0 {
		return nil, contract.Refuse(name, contract.RefusalPushFilterInvalid, "%s declares no filter", entry)
	}
	selector, err := protocol.Compile(declaration.Filter)
	if err != nil {
		code := contract.RefusalPushFilterInvalid
		if isUnknownElement(err) {
			code = contract.RefusalPushElementUnknown
		}
		return nil, contract.Refuse(name, code, "%s: %v", entry, err)
	}
	binding.selector = selector

	offered := map[string]bool{}
	for _, transport := range protocol.Transports() {
		offered[transport] = true
	}
	for _, transport := range declaration.Transports {
		if !offered[transport] {
			return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s accepts %s, which %s is not received over",
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
			return nil, contract.Refuse(name, contract.RefusalPushLivenessBelowFloor, "%s declares liveness_seconds %d, below the "+
				"floor of %d", entry, declaration.LivenessSeconds, int(push.LivenessFloor/time.Second))
		}
	}

	// How a message becomes a row.
	fieldsOf := protocol.Fields()
	for column, sources := range declaration.Fields {
		if len(sources) == 0 {
			return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s maps the column %q to no field", entry, column)
		}
		for _, source := range sources {
			if _, known := protocol.FieldType(source); !known && !push.IsDeliveryField(source) {
				return nil, contract.Refuse(name, contract.RefusalPushElementUnknown, "%s maps the column %q to %q, which %s does "+
					"not carry", entry, column, source, declaration.Protocol)
			}
		}
	}
	if declaration.Parse != "" {
		if fieldsOf == nil {
			return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s declares a parse function: a %s record "+
				"is mapped to columns by its fields, with no script", entry, declaration.Protocol)
		}
		if exports[pushParseNamespace][declaration.Parse] != "function" {
			return nil, contract.Refuse(name, contract.RefusalPushParseMissing, "%s names the parse function %s.%s, which the "+
				"script does not export", entry, pushParseNamespace, declaration.Parse)
		}
	}
	if push.IsScriptDecoded(protocol) {
		// A message the core reads no structure in is decoded by the plugin's own function, and by nothing else.
		switch {
		case declaration.Decode == "":
			return nil, contract.Refuse(name, contract.RefusalPushDecodeMissing, "%s is of the %s protocol, whose messages carry "+
				"no structure the core reads: it names a decode function", entry, declaration.Protocol)
		case declaration.Parse != "" || declaration.Pattern != "" || len(declaration.Fields) > 0:
			return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s is decoded by its decode function, and "+
				"declares a parse function, a pattern or a field mapping beside it", entry)
		case exports[pushParseNamespace][declaration.Decode] != "function":
			return nil, contract.Refuse(name, contract.RefusalPushDecodeMissing, "%s names the decode function %s.%s, which the "+
				"script does not export", entry, pushParseNamespace, declaration.Decode)
		}
	} else if declaration.Decode != "" {
		return nil, contract.Refuse(name, contract.RefusalPushDecodeMissing, "%s names a decode function: a %s message is decoded "+
			"by the core, and mapped by a pattern, a parse function or a field mapping", entry, declaration.Protocol)
	}
	if bound := declaration.SequenceBound; bound != nil && !contract.NamePattern.MatchString(bound.Value) {
		return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s takes its sequence bound from %q, which is not "+
			"a snake_case value name", entry, bound.Value)
	}
	if declaration.Pattern != "" {
		if fieldsOf == nil {
			return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s declares a pattern: a %s record carries "+
				"no message text", entry, declaration.Protocol)
		}
		if declaration.Parse != "" {
			return nil, contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s declares both a pattern and a parse "+
				"function", entry)
		}
		pattern, err := regexp.Compile(declaration.Pattern)
		if err != nil {
			return nil, contract.Refuse(name, contract.RefusalPushPatternInvalid, "%s: the pattern does not compile under RE2: %v",
				entry, err)
		}
		binding.pattern = pattern
	}

	// Where rows go.
	switch {
	case declaration.OutputTable != "" && declaration.Kind != "",
		declaration.OutputTable == "" && declaration.Kind == "":
		return nil, contract.Refuse(name, contract.RefusalPushDestinationInvalid, "%s names %s: one of output_table and kind, "+
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
		if _, declared := plugin.Endpoints[declaration.Replaces]; !declared {
			return nil, contract.Refuse(name, contract.RefusalPushReplacesUnknown, "%s replaces %q, which the manifest does not "+
				"declare as an endpoint", entry, declaration.Replaces)
		}
	}
	return binding, nil
}

func describeDestination(declaration contract.PushDeclaration) string {
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
func checkPushTable(plugin *Plugin, binding *pushBinding, tables map[string]plugindecl.PluginTable,
	pushProviders []contract.ProviderDeclaration, entry string) *contract.Refusal {
	name := plugin.Name
	declaration := binding.declaration
	table, declared := tables[declaration.OutputTable]
	if !declared {
		return contract.Refuse(name, contract.RefusalPushDestinationInvalid, "%s writes into %q, which the plugin does not declare",
			entry, declaration.OutputTable)
	}
	timeColumn, _ := tableColumn(table, table.TimeColumn)
	if table.TimeColumn == "" || timeColumn.Type != push.TypeInstant {
		return contract.Refuse(name, contract.RefusalPushTableWithoutInstant, "%s writes into %s, whose time column is not an "+
			"instant: a push output table is a hypertable chunked on an instant", entry, table.Table)
	}
	if len(pushProviders) != 1 {
		return contract.Refuse(name, contract.RefusalPushProviderMissing, "%s writes into a table of the plugin's own, which needs "+
			"exactly one provider of a kind of the plugin's own declared push, or with source push; the plugin declares %d",
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
				return contract.Refuse(name, contract.RefusalPushPatternInvalid, "%s: the named group %q is not a column of %s",
					entry, group, table.Table)
			}
		}
	}
	for column := range declaration.Fields {
		if _, isColumn := tableColumn(table, column); !isColumn {
			return contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s maps %q, which is not a column of %s", entry,
				column, table.Table)
		}
	}
	if declaration.Parse != "" || declaration.Decode != "" {
		// What the parse or decode function returns is checked per row, against the table.
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
			return contract.Refuse(name, contract.RefusalPushColumnUnfilled, "%s leaves the NOT NULL column %s of %s unfilled", entry,
				column.Name, table.Table)
		}
	}
	return nil
}

// checkPushKind checks an entry delivering records of a core kind, which the same plugin provides.
func checkPushKind(plugin *Plugin, binding *pushBinding, providedKinds map[string]contract.ProviderDeclaration,
	entry string) *contract.Refusal {
	name := plugin.Name
	declaration := binding.declaration
	provided, declared := providedKinds[declaration.Kind]
	kindContract, core := provider.ContractOf(declaration.Kind)
	if !declared || !core {
		return contract.Refuse(name, contract.RefusalPushDestinationInvalid, "%s delivers records of %q, which is not a core kind the "+
			"plugin provides", entry, declaration.Kind)
	}
	table := kindContract.PushRecord
	record, hasTable := provider.LookupRecord(kindContract, table)
	switch {
	case !kindContract.Push || !hasTable:
		return contract.Refuse(name, contract.RefusalPushDestinationInvalid, "%s delivers records of %s, which no push entry may "+
			"deliver: its kind file does not mark it push", entry, declaration.Kind)
	case declaration.Parse == "" && declaration.Decode == "" && len(declaration.Fields) == 0:
		return contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s delivers records of %s, which a parse or decode "+
			"function builds or a field mapping fills: the records are the kind's contract", entry, declaration.Kind)
	case declaration.Parse != "" && len(declaration.Fields) > 0:
		return contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s delivers records of %s with both a parse function "+
			"and a field mapping: one of the two builds them", entry, declaration.Kind)
	case declaration.Pattern != "":
		return contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s delivers records of %s with a pattern: a record of a "+
			"kind is built by a parse function or a field mapping", entry, declaration.Kind)
	}
	if len(declaration.Fields) > 0 {
		// A mapping fills contract fields, and every required one.
		for column := range declaration.Fields {
			if _, known := record.Field(column); !known {
				return contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s maps %q, which is not a field of %s", entry,
					column, table)
			}
		}
		for _, field := range record.Fields {
			if _, mapped := declaration.Fields[field.Name]; field.Required && !mapped {
				return contract.Refuse(name, contract.RefusalPushColumnUnfilled, "%s leaves the required field %s of %s unmapped",
					entry, field.Name, table)
			}
		}
	}
	// PS9, as the NetFlow join amendment's PS17 amends it. The load check tells the two cases apart by
	// the CONTRACT, never by the plugin: an entry delivers the record table <kind>_record, and the
	// kind's polled functions can return the tables PolledRecordTables lists. When that list holds the
	// entry's table, the provider delivers the same records by both paths, and the entry must name the
	// endpoint it replaces, or the two paths would count them twice (push_kind_without_replacement).
	// When it does not -- flow_record's polled function returns the capture configuration, a different
	// record -- the entry is push-only, and a `replaces` would replace nothing.
	polled := provider.PolledRecordTables(kindContract)[table]
	switch {
	case polled && declaration.Replaces == "":
		return contract.Refuse(name, contract.RefusalPushKindWithoutReplacement, "%s delivers %s records beside a provider that polls "+
			"them, and declares no replaces: without an exclusive source per instant the two paths would count "+
			"records twice", entry, declaration.Kind)
	case !polled && declaration.Replaces != "":
		return contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s replaces %q, but no polled function of %s delivers %s "+
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
		return contract.Refuse(name, contract.RefusalPushDeclarationInvalid, "%s delivers records of %s over no transport whose loss "+
			"is detectable", entry, declaration.Kind)
	}
	binding.transports = detectable
	return nil
}

// tableColumn returns a table's column by name.
func tableColumn(table plugindecl.PluginTable, name string) (plugindecl.PluginColumn, bool) {
	for _, column := range table.Columns {
		if column.Name == name {
			return column, true
		}
	}
	return plugindecl.PluginColumn{}, false
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
	if kindContract, core := provider.ContractOf(b.declaration.Kind); core {
		if record, found := provider.LookupRecord(kindContract, kindContract.PushRecord); found {
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

// ListedPush is the plugin's push entries with the receiver's view of each.
func (p *Plugin) ListedPush() []PushEntry {
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
