package main

import (
	"regexp"
	"strings"
)

// docDeclaration is a declaration line a doc comment may name: a function, a method, a type, a
// variable, a constant, or a field or spec inside a block.
var docDeclaration = regexp.MustCompile(`^(?:func (?:\([^)]*\) )?|type |var |const |\t+)([A-Za-z_][A-Za-z0-9_]*)\b`)

// docFirstWord is the first word of a comment line.
var docFirstWord = regexp.MustCompile(`^(\t*// )([A-Za-z_][A-Za-z0-9_]*)(.*)$`)

// alignDocComments makes a doc comment that begins with a declaration's old spelling begin with
// its new one: an identifier a split exported keeps a comment that names it.
func alignDocComments(source []byte) []byte {
	lines := strings.Split(string(source), "\n")
	isComment := func(line string) bool { return strings.HasPrefix(strings.TrimLeft(line, "\t"), "//") }
	for index := 1; index < len(lines); index++ {
		declared := docDeclaration.FindStringSubmatch(lines[index])
		if declared == nil || !isComment(lines[index-1]) {
			continue
		}
		first := index - 1
		for first > 0 && isComment(lines[first-1]) {
			first--
		}
		word := docFirstWord.FindStringSubmatch(lines[first])
		if word != nil && word[2] != declared[1] && strings.EqualFold(word[2], declared[1]) {
			lines[first] = word[1] + declared[1] + word[3]
		}
	}
	return []byte(strings.Join(lines, "\n"))
}
