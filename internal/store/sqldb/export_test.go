package sqldb

import "strings"

// What this package's external tests read that the package keeps unexported.

// UnreadableObjects is unreadableObjects.
var UnreadableObjects = unreadableObjects

// Role is the read connections' role.
func (c *ReadConnections) Role() string { return c.role }

// ParameterNames is the names a text's translation binds, in order.
func ParameterNames(text string) ([]string, error) {
	translation, err := Translate(text)
	return translation.Names, err
}

// IsComposeTemplate reports whether a statement is a composition template.
func IsComposeTemplate(name string) bool {
	text, err := Statement(name)
	return err == nil && strings.Contains(text, childToken)
}

// TranslationCount is how many texts were translated since the process started.
func TranslationCount() int64 { return translated.Load() }
