// Package messages holds the catalogue keys the core's Go code chooses (messages.go) and the
// availability names of a source (registry_keys.go). A handler chooses a key, never a string; the
// words are a locale file's. webtest.CoreKeys lists every key here with the registry's vocabulary,
// so a test holds each to a catalogue entry and every catalogue entry to a reader.
package messages
