package sqldb

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
