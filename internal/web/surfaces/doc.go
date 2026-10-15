// Package surfaces links every surface of the interface into the binary.
//
// A SURFACE IS A DIRECTORY BELOW THIS ONE: it holds a surface.json marker, its handlers, its
// page templates (template/), its catalogue files (locale/) and its tests, and registers
// itself with web.RegisterSurface from an init(). Adding one is adding that directory and
// running `go generate ./...`, which rewrites surfaces_gen.go -- the only file that names the
// surfaces, and a generated one. A test (TestTheGeneratedLinkFileIsCurrent) fails, naming the
// command, when the file does not match the directories, so it is never edited by hand and
// never stale. docs/extending.md carries the whole procedure with a worked example.
//
// Import this package for its effect, the way cmd/opnview does:
//
//	import _ "github.com/NaejEL/opnview/internal/web/surfaces"
package surfaces
