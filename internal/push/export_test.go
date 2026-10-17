package push

// Duplicates are the protocol names registered more than once: a test asserts there are none.
func Duplicates() []string {
	registryMutex.RLock()
	defer registryMutex.RUnlock()
	return append([]string(nil), duplicates...)
}
