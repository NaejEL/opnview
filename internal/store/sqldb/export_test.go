package sqldb

// What this package's external tests read that the package keeps unexported.

// UnreadableObjects is unreadableObjects.
var UnreadableObjects = unreadableObjects

// Role is the read connections' role.
func (c *ReadConnections) Role() string { return c.role }

// ParameterNames is the names rewriteParameters binds, in order; "" for a positional parameter.
func ParameterNames(text string) ([]string, error) {
	rewritten, err := rewriteParameters(text)
	return rewritten.names, err
}
