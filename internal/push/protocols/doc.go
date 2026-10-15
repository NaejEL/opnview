// Package protocols links every push protocol into the binary.
//
// A PROTOCOL IS A DIRECTORY BELOW THIS ONE: it holds a protocol.json marker, its decoder and its
// tests, and registers itself with push.Register from an init(). Adding one is adding that
// directory and running `go generate ./...`, which rewrites protocols_gen.go -- the only file that
// names the protocols, and a generated one. A test (TestTheGeneratedLinkFileIsCurrent) fails,
// naming the command, when the file does not match the directories.
//
// Import this package for its effect:
//
//	import _ "github.com/NaejEL/opnview/internal/push/protocols"
package protocols
