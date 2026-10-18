package widget

import (
	"errors"
	"io/fs"
	"path"

	"github.com/dop251/goja"

	"github.com/NaejEL/opnview/internal/widget/params"
	"github.com/NaejEL/opnview/internal/widget/request"
)

// A widget's renderer, and the front-only widget.
//
// A RENDERER IS A FILE OF THE WIDGET'S DIRECTORY. The manifest names it (`renderer.entry`), and the
// loader checks here, at start, that the directory holds it and that it compiles as a CLASSIC
// SCRIPT -- the one form that runs unchanged in the browser, where the front loads it with a
// <script> element, and in the interpreter the front's tests run it in (docs/front-plugins.md). An
// ES module's `import` or `export` does not compile as one, so it is refused here rather than
// failing in a reader's browser. The checked bytes are kept on the preset: the front's index
// serves exactly them, and nothing else of the directory (internal/front).
//
// A FRONT-ONLY WIDGET has a renderer and neither a query nor a script. Nothing on the server
// answers it: it is listed (Service.FrontOnlyTypes) and never served, and its type answers 404
// front_only_widget (ServeUnknown). Its renderer asks the core for what it draws.

// ConditionFrontOnlyWidget answers GET /api/widget/{type} for a front-only type.
const ConditionFrontOnlyWidget = "front_only_widget"

// loadRenderer reads and checks a manifest's renderer from the widget's directory, or answers
// nothing for a widget its shape's renderer draws.
func loadRenderer(files fs.FS, location string, manifest params.Manifest) (string, []byte, *params.Refusal) {
	if manifest.Renderer == nil {
		return "", nil, nil
	}
	entry := manifest.Renderer.Entry
	source, err := fs.ReadFile(files, entry)
	if err != nil {
		code := params.RefusalRendererInvalid
		if errors.Is(err, fs.ErrNotExist) {
			code = params.RefusalRendererMissing
		}
		return "", nil, params.Refuse(manifest.Type, code, "%s cannot be read: %v", entry, err)
	}
	if _, err := goja.Compile(path.Join(location, entry), string(source), false); err != nil {
		return "", nil, params.Refuse(manifest.Type, params.RefusalRendererInvalid,
			"%s does not compile as a classic script (no import or export, no module syntax): %v", entry, err)
	}
	return entry, source, nil
}

// frontOnlyPreset is the preset of a validated front-only manifest, its renderer checked.
func frontOnlyPreset(files fs.FS, location string, manifest params.Manifest, origin string) (request.Preset,
	*params.Refusal) {
	entry, source, refusal := loadRenderer(files, location, manifest)
	if refusal != nil {
		return request.Preset{}, refusal
	}
	preset := presetFromManifest(manifest, origin)
	preset.RendererEntry, preset.RendererSource = entry, source
	preset.FrontOnly = true
	return preset, nil
}

// withRenderer checks a served widget's renderer, when it names one, and keeps it on the preset.
func withRenderer(files fs.FS, location string, manifest params.Manifest, preset request.Preset) (request.Preset,
	*params.Refusal) {
	entry, source, refusal := loadRenderer(files, location, manifest)
	if refusal != nil {
		return request.Preset{}, refusal
	}
	preset.RendererEntry, preset.RendererSource = entry, source
	return preset, nil
}
