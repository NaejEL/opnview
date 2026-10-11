package widgets

// Coverage row 39 of docs/widget-api.md (C6 of the backend-completion cycle): each provider the
// source_availability widget lists carries, beside its condition, the bounds of when its current
// state began. That one field is new; every other byte of the widget's answer is held to the record.
func init() {
	registerPhaseCChange("source_availability", "data.providers[].since")
}
