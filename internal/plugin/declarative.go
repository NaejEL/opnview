package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/plugin/contract"
	"github.com/NaejEL/opnview/internal/plugin/scripthost"
	"github.com/NaejEL/opnview/internal/store/plugindecl"
	"github.com/NaejEL/opnview/internal/store/vocab"
)

// The declarative form: a probe and a read the manifest expresses, with no script.
//
// A COLLECTOR PLUGIN CAN BE DATA, NOT CODE, when its source is one endpoint answering rows. Its
// manifest names the endpoint, whether to send the standard grid-search page, and where each
// field of its kind's record comes from in a row; the host calls the endpoint through opnview's
// client, reads the rows exactly as the core reads a collection (decode.Rows), reads each field
// with the field reader of internal/decode its type names, and hands the kind the same answer a
// script would -- which the same adapter checks against the same contract. A row lacking a field
// the record requires is not stored and is counted in the availability detail; a field the
// record does not have is refused at load (mapping_field_unknown), and a required one left
// unmapped likewise (mapping_field_missing).
//
// What it cannot express -- a probe that reads a setting behind a service status, a timestamp
// needing the firewall's offset in a shape no reading names, a key composed of several fields --
// is what a script is for.

// The readings a mapping's `as` may name.
var readings = map[string]string{
	"epoch":           "an instant already in epoch seconds (decode.Epoch)",
	"iso_time":        "an ISO 8601 instant (the firewall's offset applied)",
	"syslog_time":     "a syslog instant (year-less; the firewall's offset applied)",
	"filter_log_time": "a filter-log instant, in either shape the filter log writes",
	"port":            "a port, 0 to 65535 (decode.Port)",
	"mac":             "a hardware address, lower-cased; any other shape is absent",
	"now":             "the pass's own instant; no `from`",
	"provider_id":     "the provider's registry id, as the pass is told it; no `from` (a core kind read into a table)",
	"provider_key":    "the provider's key; no `from` (a core kind read into a table)",
	"shape":           "the JSON shape the field arrived in: absent, null, string or other",
}

// declarativeForm is one provider's probe and read.
type declarativeForm struct {
	plugin    string
	kind      string
	endpoints map[string]opnsense.Endpoint
	probe     contract.ProbeDeclaration
	read      *contract.ReadDeclaration
	// function is the kind's read function and recordsField the field of its answer the
	// records go under; table is the record table they are checked against, or, for a kind of
	// the plugin's own, the plugin table they are written into.
	function     string
	recordsField string
	columns      map[string]string
	required     []string
}

// checkDeclarative checks a provider's declarative probe and read, and returns the form.
func checkDeclarative(plugin *Plugin, declaration contract.ProviderDeclaration, kindContract provider.Contract, core bool) (
	*declarativeForm, *contract.Refusal) {
	name := plugin.Name
	form := &declarativeForm{plugin: name, kind: declaration.Kind, endpoints: plugin.Endpoints,
		read: declaration.Read, columns: map[string]string{}}
	if declaration.Read == nil {
		return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "a declarative %s provider declares its read", declaration.Kind)
	}
	read := declaration.Read
	readEndpoint, declared := plugin.Endpoints[read.Endpoint]
	if !declared {
		return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the read names the endpoint %q, which the manifest does not "+
			"declare", read.Endpoint)
	}
	if readEndpoint.Stream {
		return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the endpoint %q streams; a declarative read reads rows",
			read.Endpoint)
	}
	form.probe = contract.ProbeDeclaration{Endpoint: read.Endpoint, Check: "answers"}
	if declaration.Probe != nil {
		form.probe = *declaration.Probe
	}
	if probeEndpoint, declared := plugin.Endpoints[form.probe.Endpoint]; !declared || probeEndpoint.Stream {
		return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the probe names the endpoint %q, which the manifest does not "+
			"declare as an endpoint answering once", form.probe.Endpoint)
	}
	if form.probe.Check != "answers" && form.probe.Check != "service_running" {
		return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the probe check %q is not answers or service_running",
			form.probe.Check)
	}

	_, collected := kindContract.FunctionOf("collect")
	if core && !collected {
		function, recordsField, table, ok := declarativeShape(kindContract)
		if !ok {
			return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the %s kind has no declarative form: its records are not "+
				"one row per row of one endpoint, so a script serves it", declaration.Kind)
		}
		if read.Table != "" {
			return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "a read of the core kind %s writes its records to the kind, "+
				"never into a table", declaration.Kind)
		}
		form.function, form.recordsField = function, recordsField
		record, _ := provider.LookupRecord(kindContract, table)
		for _, field := range record.Fields {
			form.columns[field.Name] = string(field.Type)
			if field.Required {
				form.required = append(form.required, field.Name)
			}
		}
	} else {
		// A kind read by collect -- a kind of the plugin's own, or a core kind stored by its owner's table
		// (ADR 0020 wave 4) -- writes into a table: one of the plugin's own by its name, or a table another
		// plugin granted it, by `<owner>.<table>`.
		form.function, form.recordsField = "collect", "rows"
		var table *plugindecl.PluginTable
		for index := range plugin.OwnTables {
			if plugin.OwnTables[index].Table == read.Table {
				table = &plugin.OwnTables[index]
			}
		}
		if granted, found := plugin.grantedTables[read.Table]; found {
			table = &granted
		}
		if table == nil {
			return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the read writes into %q, which is not a table the plugin "+
				"declares or is granted", read.Table)
		}
		for _, column := range table.WrittenColumns() {
			form.columns[column.Name] = map[string]string{"integer": "integer", "real": "number",
				"text": "text"}[column.Type]
			if !column.Nullable {
				form.required = append(form.required, column.Name)
			}
		}
	}
	for _, field := range scripthost.SortedKeys(read.Fields) {
		mapping := read.Fields[field]
		if _, known := form.columns[field]; !known {
			return nil, contract.Refuse(name, contract.RefusalMappingFieldUnknown, "the read maps %q, which is not a field of the %s "+
				"record", field, declaration.Kind)
		}
		if mapping.As != "" {
			if _, known := readings[mapping.As]; !known {
				return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the field %q is read as %q, which is not a reading "+
					"(%s)", field, mapping.As, strings.Join(scripthost.SortedKeys(readings), ", "))
			}
		}
		switch {
		case mapping.Value != nil && (mapping.From != "" || mapping.As != ""):
			return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the field %q is both a constant and read from a row", field)
		case mapping.Value == nil && mapping.From == "" && mapping.As != "now" && mapping.As != "provider_id" &&
			mapping.As != "provider_key":
			return nil, contract.Refuse(name, contract.RefusalMappingInvalid, "the field %q names neither a row field nor a value", field)
		}
	}
	for _, field := range form.required {
		if _, mapped := read.Fields[field]; !mapped {
			return nil, contract.Refuse(name, contract.RefusalMappingFieldMissing, "the read leaves %q unmapped, and the %s record "+
				"requires it", field, declaration.Kind)
		}
	}
	return form, nil
}

// declarativeShape is a core kind's read in the declarative form: its read function, the answer
// field the records go under and their table. A kind whose records nest -- a set of items -- or
// whose read is not one function has none.
func declarativeShape(kindContract provider.Contract) (string, string, string, bool) {
	var reads []provider.Function
	for _, function := range kindContract.Functions {
		if function.Name != "probe" && function.Name != "rotation" {
			reads = append(reads, function)
		}
	}
	if len(reads) != 1 {
		return "", "", "", false
	}
	answer, found := kindContract.Record(reads[0].Returns)
	if !found {
		return "", "", "", false
	}
	for _, field := range answer.Fields {
		if field.Type != provider.FieldRecords || field.Of == "not_record_count" {
			continue
		}
		record, found := kindContract.Record(field.Of)
		if !found {
			return "", "", "", false
		}
		for _, inner := range record.Fields {
			if inner.Type == provider.FieldRecords || inner.Type == provider.FieldRecord {
				return "", "", "", false
			}
		}
		return reads[0].Name, field.Name, field.Of, true
	}
	return "", "", "", false
}

// invoke answers one function of the kind, as a script would.
func (f *declarativeForm) invoke(ctx context.Context, session provider.Session, function string,
	arguments ...any) (any, []error, *scripthost.Failure) {
	switch function {
	case "probe":
		return f.runProbe(ctx, session), nil, nil
	case f.function:
		return f.runRead(ctx, session, function, arguments), nil, nil
	}
	return nil, nil, &scripthost.Failure{Code: scripthost.FailureScript, Detail: fmt.Sprintf("the declarative form answers no %s", function)}
}

// runProbe asks the probe endpoint.
func (f *declarativeForm) runProbe(ctx context.Context, session provider.Session) map[string]any {
	endpoint := f.endpoints[f.probe.Endpoint]
	result := map[string]any{"probe": f.probe.Endpoint, "state": string(vocab.StateUnavailable),
		"outcome": string(vocab.OutcomeRequestNotMade)}
	answer := map[string]any{"result": result}
	if f.probe.Check == "service_running" {
		running, state, outcome, detail, err := session.ServiceState(ctx, endpoint)
		if err != nil {
			answer["error"] = err
			return answer
		}
		result["state"], result["outcome"], result["separable"] = string(state), string(outcome), running
		if detail != "" {
			result["detail"] = detail
		}
		return answer
	}
	response, err := session.Call(ctx, endpoint, opnsense.RequestOptions{})
	if err != nil && response.Outcome == "" {
		answer["error"] = err
		return answer
	}
	result["outcome"] = string(provider.ResponseOutcome(response))
	if !response.OK() {
		if response.Detail != "" {
			result["detail"] = response.Detail
		}
		return answer
	}
	result["state"], result["separable"] = string(vocab.StateReachable), true
	return answer
}

// runRead reads the rows and maps them.
func (f *declarativeForm) runRead(ctx context.Context, session provider.Session, function string,
	arguments []any) map[string]any {
	endpoint := f.endpoints[f.read.Endpoint]
	result := map[string]any{"probe": f.read.Endpoint, "state": string(vocab.StateUnavailable),
		"outcome": string(vocab.OutcomeRequestNotMade)}
	answer := map[string]any{"result": result}
	pageSize, page, now := 0, 1, session.Now()
	var providerArgument map[string]any
	switch function {
	case "lookups":
		if len(arguments) > 0 {
			page, _ = arguments[0].(int)
		}
	case "sample":
		if len(arguments) > 1 {
			if instant, ok := arguments[1].(int64); ok {
				now = instant
			}
		}
	case "collect":
		if len(arguments) > 1 {
			providerArgument, _ = arguments[1].(map[string]any)
			if size, isNumber := providerArgument["page_size"].(int64); isNumber {
				pageSize = int(size)
			}
		}
	default:
		if len(arguments) > 0 {
			pageSize, _ = arguments[0].(int)
		}
	}
	if page > 1 {
		// One page is all a declarative read has: the rows of the one answer.
		result["state"], result["outcome"], result["separable"] = string(vocab.StateReachable),
			string(vocab.OutcomeHealthyEmpty), true
		answer[f.recordsField] = []any{}
		return answer
	}
	options := opnsense.RequestOptions{}
	if f.read.Paginate {
		if pageSize <= 0 {
			pageSize = 1000
		}
		options.Form = opnsense.Pagination(1, pageSize)
	}
	response, err := session.Call(ctx, endpoint, options)
	if err != nil && response.Outcome == "" {
		answer["error"] = err
		return answer
	}
	if !response.OK() {
		result["outcome"] = string(provider.ResponseOutcome(response))
		if response.Detail != "" {
			result["detail"] = response.Detail
		}
		answer["error"] = fmt.Sprintf("collect: %s answered %s", endpoint.Path, response.Outcome)
		return answer
	}
	rows, err := decode.Rows(response.Body)
	if err != nil {
		result["outcome"], result["detail"] = string(vocab.OutcomeUndecodable), "the answer holds no rows to read"
		answer["error"] = fmt.Sprintf("collect: reading %s: %v", endpoint.Path, err)
		return answer
	}
	var normaliser *decode.Normaliser
	mapped := make([]any, 0, len(rows))
	skipped := 0
	for _, row := range rows {
		record, complete := f.mapRow(ctx, session, row, now, providerArgument, &normaliser)
		if !complete {
			skipped++
			continue
		}
		mapped = append(mapped, record)
	}
	result["state"], result["outcome"], result["separable"] = string(vocab.StateReachable),
		string(vocab.OutcomeHealthy), true
	if len(mapped) == 0 {
		result["outcome"] = string(vocab.OutcomeHealthyEmpty)
	}
	detail := fmt.Sprintf("read %d rows", len(rows))
	if skipped > 0 {
		detail += fmt.Sprintf("; %d lacked a field the record requires and were not stored", skipped)
	}
	result["detail"] = detail
	if function == "collect" {
		answer["rows"] = map[string]any{f.read.Table: mapped}
	} else {
		answer[f.recordsField] = mapped
	}
	return answer
}

// mapRow maps one row; it reports false when a required field is absent.
func (f *declarativeForm) mapRow(ctx context.Context, session provider.Session, row decode.Object, now int64,
	providerArgument map[string]any, normaliser **decode.Normaliser) (map[string]any, bool) {
	record := map[string]any{}
	for _, field := range scripthost.SortedKeys(f.read.Fields) {
		mapping := f.read.Fields[field]
		value, present := f.mapField(ctx, session, row, field, mapping, now, providerArgument, normaliser)
		if !present {
			continue
		}
		record[field] = value
	}
	for _, field := range f.required {
		if _, present := record[field]; !present {
			return nil, false
		}
	}
	return record, true
}

// mapField reads one field of a row.
func (f *declarativeForm) mapField(ctx context.Context, session provider.Session, row decode.Object, field string,
	mapping contract.FieldMapping, now int64, providerArgument map[string]any, normaliser **decode.Normaliser) (any, bool) {
	if mapping.Value != nil {
		return mapping.Value, true
	}
	switch mapping.As {
	case "now":
		return now, true
	case "provider_id":
		id, present := providerArgument["id"]
		return id, present
	case "provider_key":
		key, present := providerArgument["key"]
		return key, present
	}
	steps := strings.Split(mapping.From, ".")
	container := row
	if len(steps) > 1 {
		nested, present := decode.Nested(row, steps[:len(steps)-1]...)
		if !present {
			return nil, false
		}
		container = nested
	}
	key := steps[len(steps)-1]
	timestamp := func(read func(decode.Normaliser, string) (int64, error)) (any, bool) {
		text, present := decode.String(container, key)
		if !present {
			return nil, false
		}
		if *normaliser == nil {
			measured, err := session.Normaliser(ctx)
			if err != nil {
				return nil, false
			}
			*normaliser = &measured
		}
		value, err := read(**normaliser, text)
		return value, err == nil
	}
	switch mapping.As {
	case "epoch":
		return decode.Epoch(container, key)
	case "port":
		port := decode.Port(container, key)
		if port == nil {
			return nil, false
		}
		return *port, true
	case "mac":
		mac := decode.MACPointer(decode.RawString(container, key))
		if mac == nil {
			return nil, false
		}
		return *mac, true
	case "shape":
		_, shape := decode.TextField(container, key)
		return scripthost.ShapeName(shape), true
	case "iso_time":
		return timestamp(decode.Normaliser.NormaliseISO)
	case "syslog_time":
		return timestamp(decode.Normaliser.NormaliseSyslog)
	case "filter_log_time":
		return timestamp(decode.Normaliser.NormaliseFilterLogTimestamp)
	}
	switch f.columns[field] {
	case "integer":
		return decode.Int(container, key)
	case "number":
		return decode.Float(container, key)
	case "boolean":
		flag := decode.Flag(container, key)
		if flag == nil {
			return nil, false
		}
		return *flag, true
	default:
		if field == "ttl" {
			// A resolver record's TTL is its text verbatim, whatever its shape.
			text, shape := decode.TextField(container, key)
			return text, shape == decode.ShapeString
		}
		return decode.String(container, key)
	}
}
