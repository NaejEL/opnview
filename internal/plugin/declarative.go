package plugin

import (
	"context"
	"fmt"
	"strings"

	"github.com/NaejEL/opnview/internal/collect/dhcplease"
	"github.com/NaejEL/opnview/internal/collect/provider"
	"github.com/NaejEL/opnview/internal/decode"
	"github.com/NaejEL/opnview/internal/opnsense"
	"github.com/NaejEL/opnview/internal/store"
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
	"shape":           "the JSON shape the field arrived in: absent, null, string or other",
}

// declarativeForm is one provider's probe and read.
type declarativeForm struct {
	plugin    string
	kind      string
	endpoints map[string]opnsense.Endpoint
	probe     ProbeDeclaration
	read      *ReadDeclaration
	// function is the kind's read function and recordsField the field of its answer the
	// records go under; table is the record table they are checked against, or, for a kind of
	// the plugin's own, the plugin table they are written into.
	function     string
	recordsField string
	columns      map[string]string
	required     []string
}

// checkDeclarative checks a provider's declarative probe and read, and returns the form.
func checkDeclarative(plugin *Plugin, declaration ProviderDeclaration, contract provider.Contract, core bool) (
	*declarativeForm, *Refusal) {
	name := plugin.Name
	form := &declarativeForm{plugin: name, kind: declaration.Kind, endpoints: plugin.endpoints,
		read: declaration.Read, columns: map[string]string{}}
	if declaration.Read == nil {
		return nil, refuse(name, RefusalMappingInvalid, "a declarative %s provider declares its read", declaration.Kind)
	}
	read := declaration.Read
	readEndpoint, declared := plugin.endpoints[read.Endpoint]
	if !declared {
		return nil, refuse(name, RefusalMappingInvalid, "the read names the endpoint %q, which the manifest does not "+
			"declare", read.Endpoint)
	}
	if readEndpoint.Stream {
		return nil, refuse(name, RefusalMappingInvalid, "the endpoint %q streams; a declarative read reads rows",
			read.Endpoint)
	}
	form.probe = ProbeDeclaration{Endpoint: read.Endpoint, Check: "answers"}
	if declaration.Probe != nil {
		form.probe = *declaration.Probe
	}
	if probeEndpoint, declared := plugin.endpoints[form.probe.Endpoint]; !declared || probeEndpoint.Stream {
		return nil, refuse(name, RefusalMappingInvalid, "the probe names the endpoint %q, which the manifest does not "+
			"declare as an endpoint answering once", form.probe.Endpoint)
	}
	if form.probe.Check != "answers" && form.probe.Check != "service_running" {
		return nil, refuse(name, RefusalMappingInvalid, "the probe check %q is not answers or service_running",
			form.probe.Check)
	}

	if core {
		function, recordsField, table, ok := declarativeShape(contract)
		if !ok {
			return nil, refuse(name, RefusalMappingInvalid, "the %s kind has no declarative form: its records are not "+
				"one row per row of one endpoint, so a script serves it", declaration.Kind)
		}
		if read.Table != "" {
			return nil, refuse(name, RefusalMappingInvalid, "a read of the core kind %s writes its records to the kind, "+
				"never into a table", declaration.Kind)
		}
		form.function, form.recordsField = function, recordsField
		record, _ := provider.LookupRecord(contract, table)
		for _, field := range record.Fields {
			form.columns[field.Name] = string(field.Type)
			if field.Required {
				form.required = append(form.required, field.Name)
			}
		}
	} else {
		form.function, form.recordsField = "collect", "rows"
		var table *store.PluginTable
		for index := range plugin.tables {
			if plugin.tables[index].Table == read.Table {
				table = &plugin.tables[index]
			}
		}
		if table == nil {
			return nil, refuse(name, RefusalMappingInvalid, "the read writes into %q, which is not a table the plugin "+
				"declares", read.Table)
		}
		for _, column := range table.Columns {
			form.columns[column.Name] = map[string]string{"integer": "integer", "real": "number",
				"text": "text"}[column.Type]
			if !column.Nullable {
				form.required = append(form.required, column.Name)
			}
		}
	}
	for _, field := range sortedKeys(read.Fields) {
		mapping := read.Fields[field]
		if _, known := form.columns[field]; !known {
			return nil, refuse(name, RefusalMappingFieldUnknown, "the read maps %q, which is not a field of the %s "+
				"record", field, declaration.Kind)
		}
		if mapping.As != "" {
			if _, known := readings[mapping.As]; !known {
				return nil, refuse(name, RefusalMappingInvalid, "the field %q is read as %q, which is not a reading "+
					"(%s)", field, mapping.As, strings.Join(sortedKeys(readings), ", "))
			}
		}
		switch {
		case mapping.Value != nil && (mapping.From != "" || mapping.As != ""):
			return nil, refuse(name, RefusalMappingInvalid, "the field %q is both a constant and read from a row", field)
		case mapping.Value == nil && mapping.From == "" && mapping.As != "now":
			return nil, refuse(name, RefusalMappingInvalid, "the field %q names neither a row field nor a value", field)
		}
	}
	for _, field := range form.required {
		if _, mapped := read.Fields[field]; !mapped {
			return nil, refuse(name, RefusalMappingFieldMissing, "the read leaves %q unmapped, and the %s record "+
				"requires it", field, declaration.Kind)
		}
	}
	return form, nil
}

// declarativeShape is a core kind's read in the declarative form: its read function, the answer
// field the records go under and their table. A kind whose records nest -- a set of items -- or
// whose read is not one function has none.
func declarativeShape(contract provider.Contract) (string, string, string, bool) {
	var reads []provider.Function
	for _, function := range contract.Functions {
		if function.Name != "probe" && function.Name != "rotation" {
			reads = append(reads, function)
		}
	}
	if len(reads) != 1 {
		return "", "", "", false
	}
	answer, found := contract.Record(reads[0].Returns)
	if !found {
		return "", "", "", false
	}
	for _, field := range answer.Fields {
		if field.Type != provider.FieldRecords || field.Of == "not_record_count" {
			continue
		}
		record, found := contract.Record(field.Of)
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
	arguments ...any) (any, []error, *failure) {
	switch function {
	case "probe":
		return f.runProbe(ctx, session), nil, nil
	case "rotation":
		// A declarative security_event read keeps no file list: it reports that it cannot ask,
		// so the kind declares nothing lost.
		answer := f.runProbe(ctx, session)
		answer["known"] = false
		return answer, nil, nil
	case f.function:
		return f.runRead(ctx, session, function, arguments), nil, nil
	}
	return nil, nil, &failure{code: failureScript, detail: fmt.Sprintf("the declarative form answers no %s", function)}
}

// runProbe asks the probe endpoint.
func (f *declarativeForm) runProbe(ctx context.Context, session provider.Session) map[string]any {
	endpoint := f.endpoints[f.probe.Endpoint]
	result := map[string]any{"probe": f.probe.Endpoint, "state": string(store.StateUnavailable),
		"outcome": string(store.OutcomeRequestNotMade)}
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
	result["state"], result["separable"] = string(store.StateReachable), true
	return answer
}

// runRead reads the rows and maps them.
func (f *declarativeForm) runRead(ctx context.Context, session provider.Session, function string,
	arguments []any) map[string]any {
	endpoint := f.endpoints[f.read.Endpoint]
	result := map[string]any{"probe": f.read.Endpoint, "state": string(store.StateUnavailable),
		"outcome": string(store.OutcomeRequestNotMade)}
	answer := map[string]any{"result": result}
	pageSize, page, now := 0, 1, session.Now()
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
	default:
		if len(arguments) > 0 {
			pageSize, _ = arguments[0].(int)
		}
	}
	if page > 1 {
		// One page is all a declarative read has: the rows of the one answer.
		result["state"], result["outcome"], result["separable"] = string(store.StateReachable),
			string(store.OutcomeHealthyEmpty), true
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
		result["outcome"], result["detail"] = string(store.OutcomeUndecodable), "the answer holds no rows to read"
		answer["error"] = fmt.Sprintf("collect: reading %s: %v", endpoint.Path, err)
		return answer
	}
	var normaliser *decode.Normaliser
	mapped := make([]any, 0, len(rows))
	skipped := 0
	for _, row := range rows {
		record, complete := f.mapRow(ctx, session, row, now, &normaliser)
		if !complete {
			skipped++
			continue
		}
		mapped = append(mapped, record)
	}
	result["state"], result["outcome"], result["separable"] = string(store.StateReachable),
		string(store.OutcomeHealthy), true
	if len(mapped) == 0 {
		result["outcome"] = string(store.OutcomeHealthyEmpty)
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
	normaliser **decode.Normaliser) (map[string]any, bool) {
	record := map[string]any{}
	for _, field := range sortedKeys(f.read.Fields) {
		mapping := f.read.Fields[field]
		value, present := f.mapField(ctx, session, row, field, mapping, now, normaliser)
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
	mapping FieldMapping, now int64, normaliser **decode.Normaliser) (any, bool) {
	if mapping.Value != nil {
		return mapping.Value, true
	}
	if mapping.As == "now" {
		return now, true
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
		mac := dhcplease.MACPointer(decode.RawString(container, key))
		if mac == nil {
			return nil, false
		}
		return *mac, true
	case "shape":
		_, shape := decode.TextField(container, key)
		return shapeName(shape), true
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
