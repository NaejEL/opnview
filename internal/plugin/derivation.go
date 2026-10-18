package plugin

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/push"
	"github.com/NaejEL/opnview/internal/store"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/plugintables"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// A plugin's derivations (PA4 of the plugin-API amendment to specs/SPEC-backend-completion-plugins.md;
// docs/plugins.md, "Derivations"): functions of its script, exported under `derivations`, that the collector
// runs after the collection passes they name, after the batches the plugin's own push entries store, and on
// their cadence (provider.Derivation). A run is handed what changed since its last successful run, reads
// the datasets its plugin declares, and returns its outputs, which are checked against its declaration and
// written in ONE transaction (store.WriteDerivation): its plugin's tables, its counter tables, and the
// per-connection output and window intervals of the measures its plugin declares. A run that throws, runs too
// long or returns an output it did not declare writes nothing, keeps what it was handed for the next run,
// and is recorded in its plugin's availability with the closed outcome of the failure.

// derivationNamespace is the namespace a script exports its derivations under.
const derivationNamespace = "derivations"

// derivation is one checked derivation.
type derivation struct {
	plugin      *Plugin
	declaration contract.DerivationDeclaration
	function    string
	database    *store.Store
	// tables are its output tables, by their own name; measures its output measures, by name.
	tables   map[string]plugindecl.PluginTable
	measures map[string]vocab.DeclaredMeasure
	// statements are the plugin's statements a script run may return to run, by name (ADR 0018); sqlOnly,
	// when set, is the ordered list a SQL-only derivation runs instead of a script.
	statements map[string]string
	sqlOnly    []string
	// availability is the provider a failure is recorded on.
	availabilityKind, availabilityKey string
	// providerOf is the plugin's provider key of each kind, for a measure's intervals.
	providerOf map[string]string

	run     sync.Mutex
	mutex   sync.Mutex
	pending []provider.DerivationChange
	since   int64
	wake    chan struct{}
}

// checkDerivations checks every derivation a manifest declares.
func checkDerivations(plugin *Plugin, exports map[string]map[string]string, providers []checkedProvider,
	options Options) ([]*derivation, *contract.Refusal) {
	manifest := plugin.Manifest
	if len(manifest.Derivations) == 0 {
		return nil, nil
	}
	refuse := func(format string, arguments ...any) *contract.Refusal {
		return contract.Refuse(plugin.Name, contract.RefusalDerivationInvalid, format, arguments...)
	}
	if len(providers) == 0 {
		return nil, refuse("the plugin declares derivations and no provider, whose availability a failed run is recorded in")
	}
	infos := make([]ProviderInfo, 0, len(providers))
	providerOf := map[string]string{}
	for _, declared := range providers {
		infos = append(infos, declared.info)
		if _, taken := providerOf[declared.info.Kind]; !taken {
			providerOf[declared.info.Kind] = declared.info.Key
		}
	}
	availability := freshnessOf(plugin.Name, infos)
	tables := map[string]plugindecl.PluginTable{}
	for _, table := range plugin.OwnTables {
		tables[table.Table] = table
	}
	measures := map[string]vocab.DeclaredMeasure{}
	for _, measure := range manifest.Measures {
		measures[measure.Name] = measure
	}
	entries := map[string]bool{}
	for _, entry := range manifest.Push {
		entries[entry.Name] = true
	}
	seen := map[string]bool{}
	var checked []*derivation
	for _, declaration := range manifest.Derivations {
		if !contract.NamePattern.MatchString(declaration.Name) || seen[declaration.Name] {
			return nil, refuse("the derivation name %q is not a snake_case word declared once", declaration.Name)
		}
		seen[declaration.Name] = true
		function := declaration.Function
		if function == "" {
			function = declaration.Name
		}
		if len(declaration.Statements) > 0 {
			// SQL-only (ADR 0018): the statements run in order, and no function is named or called.
			if declaration.Function != "" {
				return nil, refuse("the derivation %s names both statements and a function; it is one or the other",
					declaration.Name)
			}
			function = ""
			for _, name := range declaration.Statements {
				if _, held := plugin.statements[name]; !held {
					return nil, refuse("the derivation %s runs the statement %q, which no statement file of the plugin holds",
						declaration.Name, name)
				}
			}
		} else if exports[derivationNamespace][function] != "function" {
			return nil, refuse("the derivation %s runs %s.%s, which the script does not export", declaration.Name,
				derivationNamespace, function)
		}
		if len(declaration.AfterPasses) == 0 && len(declaration.AfterPush) == 0 && declaration.CadenceSeconds <= 0 &&
			len(declaration.AfterDerivations) == 0 {
			return nil, refuse("the derivation %s names no trigger: a pass, a push entry, a derivation or a cadence",
				declaration.Name)
		}
		for _, after := range declaration.AfterDerivations {
			requiredPlugin, requiredDerivation, scoped := strings.Cut(after, "/")
			if !scoped || !contract.NamePattern.MatchString(requiredPlugin) || !contract.NamePattern.MatchString(requiredDerivation) ||
				!slices.Contains(manifest.Requires, requiredPlugin) {
				return nil, refuse("the derivation %s runs after %q, which is not <plugin>/<derivation> of a plugin the "+
					"manifest requires", declaration.Name, after)
			}
		}
		if declaration.CadenceSeconds < 0 {
			return nil, refuse("the derivation %s has a negative cadence", declaration.Name)
		}
		for _, pass := range declaration.AfterPasses {
			kind, key, scoped := cutPass(pass)
			if !(provider.IsCoreKind(kind) || providerOf[kind] != "") || (scoped && !contract.NamePattern.MatchString(key)) {
				return nil, refuse("the derivation %s runs after %q, which is neither a core kind nor a kind one of the "+
					"plugin's providers implements, alone or followed by /<provider key>", declaration.Name, pass)
			}
		}
		for _, entry := range declaration.AfterPush {
			if !entries[entry] {
				return nil, refuse("the derivation %s runs after the push entry %q, which the plugin does not declare",
					declaration.Name, entry)
			}
		}
		for _, name := range declaration.Datasets {
			if !plugin.declaredDatasets.Admits(name) {
				return nil, refuse("the derivation %s reads the dataset %q, which the manifest's datasets does not name",
					declaration.Name, name)
			}
		}
		d := &derivation{plugin: plugin, declaration: declaration, function: function, database: options.Store,
			tables: map[string]plugindecl.PluginTable{}, measures: map[string]vocab.DeclaredMeasure{},
			statements: map[string]string{}, sqlOnly: append([]string(nil), declaration.Statements...),
			availabilityKind: availability.Kind, availabilityKey: availability.ProviderKey, providerOf: providerOf,
			wake: make(chan struct{}, 1)}
		for _, name := range declaration.Outputs.Statements {
			text, held := plugin.statements[name]
			if !held {
				return nil, refuse("the derivation %s may run the statement %q, which no statement file of the plugin holds",
					declaration.Name, name)
			}
			d.statements[name] = text
		}
		for _, name := range declaration.Outputs.Tables {
			table, owned := tables[name]
			if !owned {
				return nil, refuse("the derivation %s writes into %q, which is not one of the plugin's tables",
					declaration.Name, name)
			}
			if len(table.PrimaryKey) == 0 {
				return nil, refuse("the derivation %s writes into %s, which declares no primary key: a run written twice "+
					"must write nothing the second time", declaration.Name, name)
			}
			d.tables[name] = table
		}
		for _, name := range declaration.Outputs.Measures {
			measure, owned := measures[name]
			if !owned {
				return nil, refuse("the derivation %s writes the measure %q, which the plugin does not declare",
					declaration.Name, name)
			}
			d.measures[name] = measure
		}
		if len(d.tables) == 0 && len(d.measures) == 0 && len(d.statements) == 0 && len(d.sqlOnly) == 0 {
			return nil, refuse("the derivation %s declares no output", declaration.Name)
		}
		checked = append(checked, d)
	}
	return checked, nil
}

// cutPass splits a pass trigger into its kind and, when it names one, its provider key.
func cutPass(pass string) (kind, key string, scoped bool) {
	for index := 0; index < len(pass); index++ {
		if pass[index] == '/' {
			return pass[:index], pass[index+1:], true
		}
	}
	return pass, "", false
}

// Name is <plugin>.<derivation>.
func (d *derivation) Name() string { return d.plugin.Name + "." + d.declaration.Name }

// AfterPasses are the passes that run it.
func (d *derivation) AfterPasses() []string {
	return append([]string(nil), d.declaration.AfterPasses...)
}

// AfterDerivations are the derivations of required plugins that run it.
func (d *derivation) AfterDerivations() []string {
	return append([]string(nil), d.declaration.AfterDerivations...)
}

// Cadence is its cadence.
func (d *derivation) Cadence() time.Duration {
	return time.Duration(d.declaration.CadenceSeconds) * time.Second
}

// Wake delivers once while a change is pending.
func (d *derivation) Wake() <-chan struct{} { return d.wake }

// Note records a change and wakes the loop.
func (d *derivation) Note(change provider.DerivationChange) {
	d.mutex.Lock()
	d.pending = append(d.pending, change)
	d.mutex.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// notePush records a batch one of the plugin's push entries stored, for every derivation it runs.
// A batch is noted at the instant its last message was received, by the hub's clock.
func (p *Plugin) notePush(entry string, rows int, from, to, at time.Time) {
	for _, d := range p.derivations {
		if slices.Contains(d.declaration.AfterPush, entry) {
			d.Note(provider.DerivationChange{Trigger: provider.TriggerPush, Entry: entry, Rows: rows,
				From: from.Unix(), To: to.Unix(), At: at.Unix()})
		}
	}
}

// noteCondition records a condition the decoder detected on a stream one of the plugin's push entries is fed
// by, for every derivation that entry runs: what no row says -- an exporter that restarted -- reaches the
// derivations that read the entry's rows.
func (p *Plugin) noteCondition(entry string, problem push.Problem) {
	for _, d := range p.derivations {
		if slices.Contains(d.declaration.AfterPush, entry) {
			d.Note(provider.DerivationChange{Trigger: provider.TriggerCondition, Entry: entry, Reason: problem.Reason,
				Scope: problem.Scope, At: problem.From.Unix()})
		}
	}
}

// Run runs the derivation over the changes noted since its last successful run.
func (d *derivation) Run(ctx context.Context, session provider.Session, now int64) (provider.DerivationOutcome, error) {
	d.run.Lock()
	defer d.run.Unlock()
	d.mutex.Lock()
	taken := len(d.pending)
	changes := append([]provider.DerivationChange(nil), d.pending...)
	since := d.since
	d.mutex.Unlock()
	if len(changes) == 0 {
		if d.declaration.CadenceSeconds <= 0 {
			return provider.DerivationOutcome{}, nil
		}
		changes = []provider.DerivationChange{{Trigger: provider.TriggerCadence, At: now}}
	}
	changes = canonicalChanges(changes)
	write, failure := d.compute(ctx, session, now, since, changes)
	if failure != nil {
		return provider.DerivationOutcome{Ran: true}, failure
	}
	if d.database == nil {
		return provider.DerivationOutcome{Ran: true}, d.failed(vocab.OutcomeScriptFailed,
			"the plugin was loaded with no store to write into")
	}
	written, err := d.database.WriteDerivation(ctx, write)
	if err != nil {
		return provider.DerivationOutcome{Ran: true}, err
	}
	d.mutex.Lock()
	d.pending = d.pending[taken:]
	d.since = now
	d.mutex.Unlock()
	return provider.DerivationOutcome{Ran: true, Written: written.Rows, Instants: written.Instants}, nil
}

// canonicalChanges orders the changes a run is handed by what they are, never by when they arrived, and drops
// a change noted twice: the same changes handed in any order make the same run.
func canonicalChanges(changes []provider.DerivationChange) []provider.DerivationChange {
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		switch {
		case a.Trigger != b.Trigger:
			return a.Trigger < b.Trigger
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		case a.Provider != b.Provider:
			return a.Provider < b.Provider
		case a.Entry != b.Entry:
			return a.Entry < b.Entry
		case a.From != b.From:
			return a.From < b.From
		case a.To != b.To:
			return a.To < b.To
		case a.Rows != b.Rows:
			return a.Rows < b.Rows
		case a.Reason != b.Reason:
			return a.Reason < b.Reason
		case a.Scope != b.Scope:
			return a.Scope < b.Scope
		}
		return a.At < b.At
	})
	return slices.Compact(changes)
}

// failed is a failure of the run, recorded on the plugin's provider.
func (d *derivation) failed(outcome vocab.AvailabilityOutcome, format string, arguments ...any) *provider.DerivationFailure {
	return &provider.DerivationFailure{Outcome: outcome, ProviderKind: d.availabilityKind, ProviderKey: d.availabilityKey,
		Probe:  scriptEndpoint(d.plugin.Name, d.plugin.Manifest.Script),
		Detail: fmt.Sprintf("the derivation %s: ", d.Name()) + fmt.Sprintf(format, arguments...)}
}

// compute calls the script and checks what it returned against the declaration.
func (d *derivation) compute(ctx context.Context, session provider.Session, now, since int64,
	changes []provider.DerivationChange) (plugintables.DerivationWrite, *provider.DerivationFailure) {
	write := plugintables.DerivationWrite{Plugin: d.plugin.Name, Rows: map[string][]map[string]any{},
		Measures: map[string][]plugintables.ConnectionMeasureRow{}, Intervals: map[string][]plugintables.MeasureInterval{},
		ComputedAt: now}
	var previous any
	if since > 0 {
		previous = since
	}
	if len(d.sqlOnly) > 0 {
		// SQL-only: the statements, in order, each handed the run's :since and :now; no script.
		for _, name := range d.sqlOnly {
			write.Statements = append(write.Statements, plugintables.StatementRun{Name: name, Text: d.plugin.statements[name],
				Parameters: map[string]any{"since": previous, "now": now}})
		}
		return write, nil
	}
	if d.plugin.script == nil {
		return write, d.failed(vocab.OutcomeScriptFailed, "the plugin has no script")
	}
	handed := make([]any, 0, len(changes))
	for _, change := range changes {
		object := map[string]any{"trigger": change.Trigger, "at": change.At}
		switch change.Trigger {
		case provider.TriggerPass:
			object["kind"], object["provider"] = change.Kind, change.Provider
		case provider.TriggerPush:
			object["entry"], object["rows"], object["from"], object["to"] = change.Entry, int64(change.Rows),
				change.From, change.To
		case provider.TriggerCondition:
			object["entry"], object["reason"], object["scope"] = change.Entry, change.Reason, change.Scope
		case provider.TriggerDerivation:
			object["derivation"] = change.Entry
		}
		handed = append(handed, object)
	}
	run := map[string]any{"derivation": d.declaration.Name, "now": now, "since": previous, "changes": handed}
	answer, _, failed := d.plugin.script.RunWith(ctx, session, derivationNamespace, d.function, scripthost.RunOptions{}, run)
	if failed != nil {
		return write, d.failed(failed.Code, "%s", failed.Detail)
	}
	if answer == nil {
		return write, nil
	}
	object, isObject := answer.(map[string]any)
	if !isObject {
		return write, d.failed(vocab.OutcomeRecordsInvalid, "returned no object")
	}
	for key := range object {
		if key != "rows" && key != "measures" && key != "intervals" && key != "statements" {
			return write, d.failed(vocab.OutcomeRecordsInvalid, "returned %q, which is not rows, measures, intervals or statements",
				key)
		}
	}
	if failure := d.statementRuns(object["statements"], &write); failure != nil {
		return write, failure
	}
	if failure := d.tableRows(object["rows"], write.Rows); failure != nil {
		return write, failure
	}
	if failure := d.measureRows(object["measures"], write.Measures); failure != nil {
		return write, failure
	}
	if failure := d.intervals(ctx, object["intervals"], &write); failure != nil {
		return write, failure
	}
	return write, nil
}

// statementRuns checks the statements a run returned to run: each {name, parameters}, the name one the
// declaration's outputs name, run in the order returned before the rows are written.
func (d *derivation) statementRuns(value any, write *plugintables.DerivationWrite) *provider.DerivationFailure {
	if value == nil {
		return nil
	}
	list, isList := value.([]any)
	if !isList {
		return d.failed(vocab.OutcomeRecordsInvalid, "statements is not a list of {name, parameters}")
	}
	for index, item := range list {
		object, isObject := item.(map[string]any)
		name, _ := object["name"].(string)
		if !isObject || name == "" {
			return d.failed(vocab.OutcomeRecordsInvalid, "statements[%d] is not {name, parameters}", index)
		}
		text, allowed := d.statements[name]
		if !allowed {
			return d.failed(vocab.OutcomeRecordsInvalid, "statements[%d] runs %q, which it does not declare as an output", index, name)
		}
		parameters := map[string]any{}
		if given, present := object["parameters"]; present && given != nil {
			typed, isMap := given.(map[string]any)
			if !isMap {
				return d.failed(vocab.OutcomeRecordsInvalid, "statements[%d].parameters is not an object", index)
			}
			parameters = typed
		}
		for key := range object {
			if key != "name" && key != "parameters" {
				return d.failed(vocab.OutcomeRecordsInvalid, "statements[%d] holds %q, which is not name or parameters", index, key)
			}
		}
		write.Statements = append(write.Statements, plugintables.StatementRun{Name: name, Text: text, Parameters: parameters})
	}
	return nil
}

// tableRows checks the rows a run returned for its tables, typing each by its table.
func (d *derivation) tableRows(value any, into map[string][]map[string]any) *provider.DerivationFailure {
	if value == nil {
		return nil
	}
	byTable, isObject := value.(map[string]any)
	if !isObject {
		return d.failed(vocab.OutcomeRecordsInvalid, "rows is not an object of tables")
	}
	for name, listed := range byTable {
		table, declared := d.tables[name]
		if !declared {
			return d.failed(vocab.OutcomeRecordsInvalid, "wrote into %q, which it does not declare as an output", name)
		}
		list, isList := listed.([]any)
		if !isList {
			return d.failed(vocab.OutcomeRecordsInvalid, "rows.%s is not a list", name)
		}
		for index, item := range list {
			row, isRow := item.(map[string]any)
			if !isRow {
				return d.failed(vocab.OutcomeRecordsInvalid, "rows.%s[%d] is not an object", name, index)
			}
			typed := make(map[string]any, len(table.Columns))
			for column := range row {
				declared, isColumn := tableColumn(table, column)
				if !isColumn {
					return d.failed(vocab.OutcomeRecordsInvalid, "rows.%s[%d] holds %q, which is not a column", name, index,
						column)
				}
				if !declared.Written() {
					return d.failed(vocab.OutcomeRecordsInvalid, "rows.%s[%d] holds %q, which the database fills: an identity or "+
						"a generated column is never written", name, index, column)
				}
			}
			for _, column := range table.WrittenColumns() {
				converted, err := push.Convert(column.Type, row[column.Name])
				if err != nil || (converted == nil && !column.Nullable) {
					return d.failed(vocab.OutcomeRecordsInvalid, "rows.%s[%d].%s is not a(n) %s", name, index, column.Name,
						column.Type)
				}
				typed[column.Name] = converted
			}
			into[name] = append(into[name], typed)
		}
	}
	return nil
}

// measureRows checks the per-connection rows a run returned for its measures.
func (d *derivation) measureRows(value any, into map[string][]plugintables.ConnectionMeasureRow) *provider.DerivationFailure {
	if value == nil {
		return nil
	}
	byMeasure, isObject := value.(map[string]any)
	if !isObject {
		return d.failed(vocab.OutcomeRecordsInvalid, "measures is not an object of measures")
	}
	for name, listed := range byMeasure {
		measure, declared := d.measures[name]
		if !declared {
			return d.failed(vocab.OutcomeRecordsInvalid, "wrote the measure %q, which it does not declare as an output", name)
		}
		list, isList := listed.([]any)
		if !isList {
			return d.failed(vocab.OutcomeRecordsInvalid, "measures.%s is not a list", name)
		}
		for index, item := range list {
			row, failure := d.measureRow(measure, item, fmt.Sprintf("measures.%s[%d]", name, index))
			if failure != nil {
				return failure
			}
			into[name] = append(into[name], row)
		}
	}
	return nil
}

// measureRowFields are the fields of a measure row a derivation returns.
var measureRowFields = map[string]bool{"connection": true, "direction": true, "connection_start_at": true,
	"state": true, "bytes": true, "packets": true, "records": true, "markers": true, "due_at": true}

// measureRow checks one per-connection row.
func (d *derivation) measureRow(measure vocab.DeclaredMeasure, item any, path string) (plugintables.ConnectionMeasureRow,
	*provider.DerivationFailure) {
	var row plugintables.ConnectionMeasureRow
	object, isObject := item.(map[string]any)
	if !isObject {
		return row, d.failed(vocab.OutcomeRecordsInvalid, "%s is not an object", path)
	}
	if remove, present := object["remove"]; present {
		// A row to remove: a connection the derivation no longer gives a figure, its held row deleted.
		connection, whole := scripthost.IntegerOf(object["connection"])
		direction, _ := object["direction"].(string)
		if remove != true || len(object) != 3 || !whole || connection < 0 || (direction != "forward" && direction != "reverse") {
			return row, d.failed(vocab.OutcomeRecordsInvalid, "%s removes a row: it is {connection, direction, remove: true}",
				path)
		}
		return plugintables.ConnectionMeasureRow{ConnectionFlowID: connection, Direction: direction, Remove: true}, nil
	}
	for field := range object {
		if !measureRowFields[field] {
			return row, d.failed(vocab.OutcomeRecordsInvalid, "%s holds %q, which a measure row does not have", path, field)
		}
	}
	whole := func(field string, required bool) (*int64, bool) {
		value, present := object[field]
		if !present || value == nil {
			return nil, !required
		}
		number, ok := scripthost.IntegerOf(value)
		if !ok || number < 0 {
			return nil, false
		}
		return &number, true
	}
	for _, field := range []string{"connection", "connection_start_at", "bytes", "packets", "records"} {
		number, ok := whole(field, true)
		if !ok {
			return row, d.failed(vocab.OutcomeRecordsInvalid, "%s.%s is not a whole number of 0 or more", path, field)
		}
		switch field {
		case "connection":
			row.ConnectionFlowID = *number
		case "connection_start_at":
			row.ConnectionStartAt = *number
		case "bytes":
			row.Bytes = *number
		case "packets":
			row.Packets = *number
		case "records":
			row.Records = *number
		}
	}
	due, ok := whole("due_at", false)
	if !ok {
		return row, d.failed(vocab.OutcomeRecordsInvalid, "%s.due_at is not an instant", path)
	}
	row.DueAt = due
	row.Direction, _ = object["direction"].(string)
	if row.Direction != "forward" && row.Direction != "reverse" {
		return row, d.failed(vocab.OutcomeRecordsInvalid, "%s.direction is not forward or reverse", path)
	}
	row.State, _ = object["state"].(string)
	if !slices.ContainsFunc(measure.States, func(state vocab.DeclaredMeasureState) bool { return state.Name == row.State }) {
		return row, d.failed(vocab.OutcomeRecordsInvalid, "%s.state %q is not a state the measure %s declares", path,
			row.State, measure.Name)
	}
	if markers, present := object["markers"]; present && markers != nil {
		list, isList := markers.([]any)
		if !isList {
			return row, d.failed(vocab.OutcomeRecordsInvalid, "%s.markers is not a list of marker names", path)
		}
		for _, marker := range list {
			name, _ := marker.(string)
			position := slices.Index(measure.Markers, name)
			if position < 0 {
				return row, d.failed(vocab.OutcomeRecordsInvalid, "%s.markers names %v, which the measure %s does not declare",
					path, marker, measure.Name)
			}
			row.Markers |= 1 << position
		}
	}
	if row.Records == 0 && (row.Bytes != 0 || row.Packets != 0) {
		return row, d.failed(vocab.OutcomeRecordsInvalid, "%s counts bytes or packets in no record", path)
	}
	return row, nil
}

// intervals checks the window intervals a run returned for its measures, for the plugin's provider of each
// measure's kind.
func (d *derivation) intervals(ctx context.Context, value any, write *plugintables.DerivationWrite) *provider.DerivationFailure {
	if value == nil {
		return nil
	}
	byMeasure, isObject := value.(map[string]any)
	if !isObject {
		return d.failed(vocab.OutcomeRecordsInvalid, "intervals is not an object of measures")
	}
	for name, listed := range byMeasure {
		measure, declared := d.measures[name]
		if !declared {
			return d.failed(vocab.OutcomeRecordsInvalid, "recorded intervals of %q, which it does not declare as an output", name)
		}
		key, provides := d.providerOf[measure.ProviderKind]
		if !provides {
			return d.failed(vocab.OutcomeRecordsInvalid, "recorded intervals of %s, whose kind %s the plugin provides no "+
				"provider of", name, measure.ProviderKind)
		}
		if d.database == nil {
			return d.failed(vocab.OutcomeScriptFailed, "the plugin was loaded with no store to write into")
		}
		providerID, err := d.database.ProviderID(ctx, measure.ProviderKind, key)
		if err != nil {
			return d.failed(vocab.OutcomeScriptFailed, "the provider %s %s is not registered: %v", measure.ProviderKind, key, err)
		}
		if write.IntervalProvider != 0 && write.IntervalProvider != providerID {
			return d.failed(vocab.OutcomeRecordsInvalid, "recorded intervals for two providers in one run")
		}
		write.IntervalProvider = providerID
		allowed := append([]string{measure.WindowStates.Covered}, measure.WindowStates.Own...)
		list, isList := listed.([]any)
		if !isList {
			return d.failed(vocab.OutcomeRecordsInvalid, "intervals.%s is not a list", name)
		}
		intervals := []plugintables.MeasureInterval{}
		for index, item := range list {
			object, isObject := item.(map[string]any)
			starts, startKnown := scripthost.IntegerOf(object["starts_at"])
			state, _ := object["state"].(string)
			if !isObject || !startKnown || starts < 0 || !slices.Contains(allowed, state) {
				return d.failed(vocab.OutcomeRecordsInvalid, "intervals.%s[%d] is not {starts_at, ends_at, state} with a "+
					"state of %v", name, index, allowed)
			}
			interval := plugintables.MeasureInterval{StartsAt: starts, State: state}
			if ends, present := object["ends_at"]; present && ends != nil {
				end, ok := scripthost.IntegerOf(ends)
				if !ok || end <= starts {
					return d.failed(vocab.OutcomeRecordsInvalid, "intervals.%s[%d].ends_at is not after starts_at", name, index)
				}
				interval.EndsAt = &end
			}
			intervals = append(intervals, interval)
		}
		write.Intervals[name] = intervals
	}
	return nil
}
