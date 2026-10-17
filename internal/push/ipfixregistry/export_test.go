package ipfixregistry

// Len is how many elements the registry holds.
func Len() int {
	loadOnce.Do(load)
	return len(byID)
}

// Err is the error reading the embedded registry met, or nil.
func Err() error {
	loadOnce.Do(load)
	return loadErr
}
