package plugin

import "fmt"

// The named reasons a plugin is refused, a closed vocabulary docs/plugins.md lists with what each
// means. A refused plugin is not loaded, the reason is logged and reported by the plugin listing,
// and nothing else changes: the other plugins load and the service starts.
const (
	// RefusalManifestUnreadable is a directory whose plugin.json cannot be read.
	RefusalManifestUnreadable = "manifest_unreadable"
	// RefusalManifestInvalid is a plugin.json that is not a valid manifest: malformed JSON, an
	// unknown key, a missing or ill-formed value.
	RefusalManifestInvalid = "manifest_invalid"
	// RefusalUnsupportedAPIVersion is a plugin targeting a plugin API version this build does
	// not read.
	RefusalUnsupportedAPIVersion = "unsupported_api_version"
	// RefusalNameMismatch is a manifest whose name is not its directory's.
	RefusalNameMismatch = "name_mismatch"
	// RefusalNameCollision is a plugin whose name a built-in plugin, or another plugin of the
	// data directory, already has. The built-in wins.
	RefusalNameCollision = "name_collision"
	// RefusalUnknownKind is a provider of a kind that is neither a core kind nor a kind the
	// plugin declares with tables of its own.
	RefusalUnknownKind = "unknown_kind"
	// RefusalCoreOnlyKind is a provider of a kind only opnview's core implements.
	RefusalCoreOnlyKind = "core_only_kind"
	// RefusalProviderKeyCollision is a provider whose kind and key another provider has.
	RefusalProviderKeyCollision = "provider_key_collision"
	// RefusalCadenceInvalid is a cadence missing where the kind needs one, declared where it
	// has a surveyed one, or below its own lower bound.
	RefusalCadenceInvalid = "cadence_invalid"
	// RefusalEndpointNotReadAction is a declared POST to a command that is not a read action: one
	// that starts with search, get, query or list, or ends with status (A13 of the broker amendment).
	RefusalEndpointNotReadAction = "endpoint_not_read_action"
	// RefusalMirrorCorrectionInvalid is a mirror correction naming a role, a type, a nature, a field
	// path or a parameter in a malformed way, or a broker key out of range (A10, A17).
	RefusalMirrorCorrectionInvalid = "mirror_correction_invalid"
	// RefusalTriggerInvalid is a triggered_by naming a channel the core does not publish, or declared
	// by a provider whose kind runs at a surveyed interval.
	RefusalTriggerInvalid = "trigger_invalid"
	// RefusalEndpointInvalid is an endpoint declaration with an ill-formed name, body or
	// argument, or a name declared twice.
	RefusalEndpointInvalid = "endpoint_invalid"
	// RefusalScriptMissing is a script the manifest names that cannot be read, or a provider
	// served by a script when the manifest names none.
	RefusalScriptMissing = "script_missing"
	// RefusalScriptDoesNotCompile is a script that is not valid JavaScript, or that fails while
	// it defines its exports.
	RefusalScriptDoesNotCompile = "script_does_not_compile"
	// RefusalScriptMissingFunction is a script that does not export a function its kind's
	// contract requires.
	RefusalScriptMissingFunction = "script_missing_function"
	// RefusalMappingInvalid is a declarative probe or read naming an endpoint the plugin does
	// not declare, a check or a reading that does not exist, or a kind with no declarative form.
	RefusalMappingInvalid = "mapping_invalid"
	// RefusalMappingFieldUnknown is a declarative read mapping a field its record does not have.
	RefusalMappingFieldUnknown = "mapping_field_unknown"
	// RefusalMappingFieldMissing is a declarative read leaving a required field unmapped.
	RefusalMappingFieldMissing = "mapping_field_missing"
	// RefusalTableInvalid is a table declaration the store refuses: an ill-formed name, a
	// column of an unknown type, a key or an index naming no column, a retention reading no
	// instant.
	RefusalTableInvalid = "table_invalid"
	// RefusalUniqueKeyWithoutTime is a table with a time column whose primary key or a unique index
	// omits it: the table is a hypertable on that column, and TimescaleDB requires it in every
	// unique key.
	RefusalUniqueKeyWithoutTime = "unique_key_without_time"
	// RefusalWidgetMissing is a widget the manifest names whose directory does not exist.
	RefusalWidgetMissing = "widget_missing"
	// RefusalStringOutsideNamespace is a catalogue string a plugin's locale/ defines under a key that
	// is not its own: plugin.<name>.*, provider.<kind>.<key> of a provider it declares, or
	// collection.kind.<kind> of a kind of its own. A plugin cannot redefine a word of the core's.
	RefusalStringOutsideNamespace = "string_outside_namespace"
	// RefusalLivePassInvalid is a live pass declared by a provider of a kind that has none, by a
	// provider no script serves, naming a function the script does not export, a cadence setting
	// that is not a declared integer setting with a default and a lower bound of at least one
	// second, or an endpoint the manifest does not declare or declares as a stream.
	RefusalLivePassInvalid = "live_pass_invalid"
	// The refusals of a push declaration (docs/plugins.md, "Push sources").
	//
	// RefusalPushDeclarationInvalid is a push entry whose shape is wrong: no name or a name twice, a
	// transport its protocol is not received over, a regular expression and a parse function both
	// or neither for syslog, a parse function or a pattern for NetFlow, or a field mapping naming
	// no field.
	RefusalPushDeclarationInvalid = "push_declaration_invalid"
	// RefusalPushProtocolUnknown is a protocol no registered decoder serves.
	RefusalPushProtocolUnknown = "push_protocol_unknown"
	// RefusalPushFilterInvalid is a header filter its protocol does not read.
	RefusalPushFilterInvalid = "push_filter_invalid"
	// RefusalPushElementUnknown is a field name the protocol does not carry: a syslog header field
	// outside its list, or an Information Element outside the IANA registry.
	RefusalPushElementUnknown = "push_element_unknown"
	// RefusalPushPatternInvalid is a regular expression that does not compile under RE2, or a named
	// group that is not a column of the output table.
	RefusalPushPatternInvalid = "push_pattern_invalid"
	// RefusalPushDestinationInvalid is an entry naming both or neither of output_table and kind, an
	// output table the plugin does not declare, or a kind none of its providers implements.
	RefusalPushDestinationInvalid = "push_destination_invalid"
	// RefusalPushTableWithoutInstant is an output table whose time column is not an instant.
	RefusalPushTableWithoutInstant = "push_table_without_instant"
	// RefusalPushKindWithoutReplacement is an entry feeding a core kind its plugin also reads through
	// a polled endpoint, with no replaces: two paths of one provider would count records twice.
	RefusalPushKindWithoutReplacement = "push_kind_without_replacement"
	// RefusalPushReplacesUnknown is a replaces naming no endpoint of the manifest.
	RefusalPushReplacesUnknown = "push_replaces_unknown"
	// RefusalPushLivenessBelowFloor is a liveness span below the floor.
	RefusalPushLivenessBelowFloor = "push_liveness_below_floor"
	// RefusalPushProviderMissing is an output table entry in a plugin declaring no provider of a
	// kind of its own with source push, which the entry's availability and gaps belong to.
	RefusalPushProviderMissing = "push_provider_missing"
	// RefusalPushColumnUnfilled is a NOT NULL column of the output table that neither the pattern,
	// the field mapping, the parse function nor the record's instant fills.
	RefusalPushColumnUnfilled = "push_column_unfilled"
	// RefusalPushParseMissing is a parse function the script does not export under push.
	RefusalPushParseMissing = "push_parse_missing"
)

// Refusal is one plugin the loader would not load, with its closed code.
type Refusal struct {
	// Plugin is the plugin's directory name.
	Plugin string
	// Origin is where it was read from: builtin or data_directory.
	Origin string
	// Code is one of the Refusal constants, an opnsense endpoint code, or a widget refusal
	// code (docs/plugins.md, "Refusals").
	Code string
	// Detail says what, precisely, was wrong.
	Detail string
}

// Error implements error.
func (r *Refusal) Error() string {
	return fmt.Sprintf("plugin %q refused (%s): %s", r.Plugin, r.Code, r.Detail)
}

// refuse builds a Refusal.
func refuse(plugin, code, format string, arguments ...any) *Refusal {
	return &Refusal{Plugin: plugin, Code: code, Detail: fmt.Sprintf(format, arguments...)}
}
