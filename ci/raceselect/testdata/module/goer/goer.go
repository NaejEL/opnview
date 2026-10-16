package goer

// Start runs f in a goroutine of its own.
func Start(f func()) { go f() }
