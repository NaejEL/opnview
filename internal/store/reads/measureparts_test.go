package reads

import (
	"encoding/json"
	"fmt"
	"testing"
)

// TestAMeasuresPartsAreReadAsTheJSONDecoderReadsThem is PA18.2's fast read of a measure's parts: the
// text PostgreSQL writes for a JSON array of whole numbers is read without the JSON decoder, to the
// numbers the decoder reads, and anything else is the decoder's -- its answer, or its refusal.
func TestAMeasuresPartsAreReadAsTheJSONDecoderReadsThem(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		`[]`, ` [ ] `, `[0]`, `[1, 0, 42]`, `[1,2,3]`, "[\n1,\t2 ]", `[-7, 9223372036854775807]`,
		`[-9223372036854775808]`, `[-0]`,
		// What the fast read leaves to the decoder.
		`[9223372036854775808]`, `[-9223372036854775809]`, `[1.5]`, `[1e3]`, `[01]`, `[-]`, `[1,]`, `[,1]`,
		`[1 2]`, `["1"]`, `[null]`, `[1]x`, `{}`, `null`, ``, `[`, `[1`, `[[1]]`, `[true]`,
	} {
		var decoded []int64
		decodeErr := json.Unmarshal([]byte(text), &decoded)
		fast, plain := plainIntegers([]byte(text))
		if !plain {
			continue
		}
		if decodeErr != nil {
			t.Errorf("%q: the fast read accepts what the decoder refuses (%v)", text, decodeErr)
			continue
		}
		if fmt.Sprint(fast) != fmt.Sprint(decoded) || (fast == nil) != (decoded == nil) {
			t.Errorf("%q: the fast read gives %v, the decoder %v", text, fast, decoded)
		}
	}
	for _, text := range []string{`[]`, `[1, 0, 42]`, `[-9223372036854775808, 9223372036854775807]`} {
		if _, plain := plainIntegers([]byte(text)); !plain {
			t.Errorf("%q, as PostgreSQL writes a measure's parts, is not read by the fast path", text)
		}
	}
}
