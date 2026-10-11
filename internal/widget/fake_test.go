package widget

// A preset added the way every preset is added -- one file, one registry row -- and
// nothing else changed: the test-only half of AC2. It lives in a test file, so the
// product never serves it.

var fakePreset = Preset{
	Type: "example_fake_widget", Shape: "figure",
	Params: []Param{{Key: "echo", Kind: KindEnum, Values: []string{"x", "y"}, Default: "y", Data: true}},
	Serve: func(r *Request) (any, bool, error) {
		return map[string]any{"echo": r.params.text("echo")}, true, nil
	},
}

func init() { registry = append(registry, fakePreset) }
