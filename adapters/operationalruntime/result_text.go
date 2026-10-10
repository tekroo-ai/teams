package operationalruntime

import "unicode/utf8"

// JSON Schema maxLength counts Unicode code points, not UTF-8 bytes. Use this
// only for schema-bounded result prose; byte-bound transport fields stay bytes.
func resultTextWithinLimit(value string, maximum int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maximum
}
