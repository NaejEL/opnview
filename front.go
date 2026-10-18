package opnview

import (
	"embed"
	"io/fs"
)

// The built-in front: the shell, the core scripts and stylesheet, the views, the shape
// renderers, the theme files, the token set and the front's own catalogue strings, one tree of
// front/ in the repository (docs/front-plugins.md).
//
// THIS FILE IS AT THE ROOT FOR THE REASON plugins.go IS: front/ holds no Go, and Go's embed
// directive cannot reach outside the directory of the file that carries it. Nothing here names
// a file of the tree: the front's index (internal/front) decides what is served, and serves
// only what it declares.
//
//go:embed front
var frontTree embed.FS

// BuiltinFront is the built-in front, rooted at front/.
func BuiltinFront() fs.FS {
	tree, err := fs.Sub(frontTree, "front")
	if err != nil {
		// fs.Sub fails only on an invalid path, and "front" is a valid one.
		return frontTree
	}
	return tree
}
