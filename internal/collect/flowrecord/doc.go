// Package flowrecord is the flow_record kind: the Flow Records an exporter sends (RFC 3954, RFC
// 7011), and the exporter's capture configuration, which says where they were observed.
//
// WHAT THE KIND PRODUCES. Two different things, by two different paths (PS17 of the NetFlow join
// amendment to specs/SPEC-backend-completion-plugins.md):
//
//   - Records, which the exporter PUSHES and opnview receives: a push entry of the provider's plugin
//     maps each one from IANA Information Elements to the fields of Record, with no script. The core
//     composes its identity and adds, at reception, its Observation Point and its direction there
//     (internal/collect/derive/flowrecord.go): neither is a field the provider fills.
//   - The capture configuration, which the provider READS by polling, through the broker: which
//     interfaces the exporter captures on, and in which directions. It is the only source of an
//     Observation Point. Nothing about one is ever inferred from the records.
//
// THE TERM. A flow_record is RFC 3954's and RFC 7011's Flow Record; it is not a `flow`, which is one
// filter-log record (docs/data-model.md, *Vocabulary*).
//
// WHAT AN IMPLEMENTATION IS. A plugin directory (docs/plugins.md) whose manifest declares a provider
// of the kind flow_record, whose script exports flow_record.probe and flow_record.capture, and whose
// push entry names the kind and maps the elements. Adding an exporter is adding such a directory:
// the core's join, coverage and states read the fields below and nothing of any exporter.
package flowrecord
