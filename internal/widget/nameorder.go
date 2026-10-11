package widget

import (
	"hash/fnv"
)

// NameFreeOrder is the tie-break between rows whose name is withheld (aggregate mode
// no_domains). Ordering withheld rows by their name, even without showing it, tells
// the reader each one's alphabetical rank; ordering them by a hash of the name tells
// nothing about the name and still gives the same order on every request.
func NameFreeOrder(name string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(name))
	return hash.Sum64()
}
