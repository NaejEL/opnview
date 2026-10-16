package store

// SQLFiles is the store's embedded SQL, for this package's external tests.
var SQLFiles = sqlFiles

// CloneStrategy is cloneStrategy.
func CloneStrategy() (string, error) { return cloneStrategy() }
