package library

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// A library's exports are bound under its plugin's name; two files of one plugin export into one
// object; a plugin that ships none, or that the caller does not require, is a TypeError naming the
// refusal, which a script may catch.
func TestLibrariesAreBoundByPluginAndAnUnknownOneIsARefusalTheScriptCanCatch(t *testing.T) {
	t.Parallel()
	first, err := Compile("figures", "a.js", "exports.one = function () { return 1; };")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile("figures", "b.js", "exports.two = function () { return exports.one() + 1; };")
	if err != nil {
		t.Fatal(err)
	}
	vm := goja.New()
	lib, err := Bind(vm, []Library{first, second})
	if err != nil {
		t.Fatal(err)
	}
	_ = vm.Set("lib", lib)
	value, err := vm.RunString("lib.figures.two()")
	if err != nil || value.ToInteger() != 2 {
		t.Fatalf("lib.figures.two() = %v, %v; want 2", value, err)
	}
	caught, err := vm.RunString(`(function () { try { lib.other.x; } catch (e) { return (e instanceof TypeError) + ":" + e.message; } return "nothing"; })()`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(caught.String(), "true:"+RefusalNotRequired) {
		t.Errorf("reading a library the script may not use: %s", caught)
	}
	keys, _ := vm.RunString("Object.keys(lib).join(',')")
	if keys.String() != "figures" {
		t.Errorf("the libraries bound are %q, want figures", keys)
	}
}

// A library that does not compile, or throws while defining its exports, is an error with the file's
// own line number, not a shifted one.
func TestALibraryThatDoesNotDefineItsExportsIsAnErrorNamingItsLine(t *testing.T) {
	t.Parallel()
	if _, err := Compile("figures", "bad.js", "exports.x = ;"); err == nil || !strings.Contains(err.Error(), "bad.js") {
		t.Errorf("a syntax error: %v", err)
	}
	throwing, err := Compile("figures", "throws.js", "exports.x = 1;\nthrow new Error('at line two');")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Bind(goja.New(), []Library{throwing})
	if err == nil || !strings.Contains(err.Error(), "at line two") || !strings.Contains(err.Error(), "throws.js:2") {
		t.Errorf("a library throwing while defining its exports: %v", err)
	}
}
