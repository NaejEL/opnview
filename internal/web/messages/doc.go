// Package messages holds the catalogue keys the core's Go code chooses (messages.go), the
// registry's vocabulary (messages_registry.go), and the keys composed from a registry row, with the
// availability names of a source (registry_keys.go). A handler chooses a key, never a string; the
// words are a locale file's. CoreKeys lists every key here, so a test holds each to a catalogue
// entry and every catalogue entry to a reader.
package messages
