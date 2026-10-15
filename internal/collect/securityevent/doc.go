// Package securityevent is the security_event kind: a source of intrusion-detection events.
//
// WHAT THE KIND PRODUCES. Alerts, newest first, each decoded and normalised by its
// implementation, plus a view of the rotation of the files the feed is written to. The
// ingestion coordinate (FileID, ByteOffset) is what this kind's cursor is keyed on; it is not a
// column of the event. The kind admits several concurrently active providers, because people run
// Suricata, CrowdSec and Zenarmor together: every event row says which provider reported it.
//
// WHAT AN IMPLEMENTATION IS. A directory internal/collect/securityevent/<product>/ holding a
// provider.json whose kind is "security_event", a Go package whose init() calls Register with the
// embedded manifest and a Source, its fixtures and its tests. See docs/extending.md. Keeping the
// watermark, detecting a lost rotation and resolving a client are the kind's business, written
// once in internal/collect (securityevent.go).
package securityevent
