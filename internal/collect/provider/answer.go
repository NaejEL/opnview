package provider

// The typed readers of a checked answer: what a kind's coordinator reads a record with. Each answers
// the zero value, or nil for a pointer, for a field the answer does not hold -- an optional field the
// implementation left absent, which is never read as a zero by a pointer reader.

// Text reads a text field.
func Text(record map[string]any, name string) string {
	value, _ := record[name].(string)
	return value
}

// TextPointer reads an optional text field: nil when absent.
func TextPointer(record map[string]any, name string) *string {
	value, present := record[name].(string)
	if !present {
		return nil
	}
	return &value
}

// Integer reads an integer field.
func Integer(record map[string]any, name string) int64 {
	value, _ := record[name].(int64)
	return value
}

// IntegerPointer reads an optional integer field: nil when absent.
func IntegerPointer(record map[string]any, name string) *int64 {
	value, present := record[name].(int64)
	if !present {
		return nil
	}
	return &value
}

// Number reads a number field.
func Number(record map[string]any, name string) float64 {
	value, _ := record[name].(float64)
	return value
}

// NumberPointer reads an optional number field: nil when absent.
func NumberPointer(record map[string]any, name string) *float64 {
	value, present := record[name].(float64)
	if !present {
		return nil
	}
	return &value
}

// Flag reads a boolean field, absent when the answer does not hold it.
func Flag(record map[string]any, name string, absent bool) bool {
	value, present := record[name].(bool)
	if !present {
		return absent
	}
	return value
}

// Records reads a records field.
func Records(record map[string]any, name string) []map[string]any {
	value, _ := record[name].([]map[string]any)
	return value
}

// Texts reads a text-list field.
func Texts(record map[string]any, name string) []string {
	value, _ := record[name].([]string)
	return value
}
