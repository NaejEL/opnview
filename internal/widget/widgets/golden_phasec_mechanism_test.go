package widgets

import (
	"testing"
)

// TestAPhaseCExceptionRemovesExactlyItsNamedPaths proves the golden exception is by name: the paths
// registered for a type are removed, every other byte is kept in the order and the number form it
// was written in, and a type with no registered path is returned unchanged.
func TestAPhaseCExceptionRemovesExactlyItsNamedPaths(t *testing.T) {
	const answer = `{"type":"t","data":{"rows":[{"name":"a","kept":1.50,"gone":2},{"name":"b","gone":3}],"total":4,"chips":[1,2],"a":true}}`
	phaseCMutex.Lock()
	saved := phaseCChanged["phase_c_mechanism_probe"]
	phaseCChanged["phase_c_mechanism_probe"] = []string{"data.rows[].gone", "data.chips[]", "data.absent.path"}
	phaseCMutex.Unlock()
	defer func() {
		phaseCMutex.Lock()
		if saved == nil {
			delete(phaseCChanged, "phase_c_mechanism_probe")
		} else {
			phaseCChanged["phase_c_mechanism_probe"] = saved
		}
		phaseCMutex.Unlock()
	}()

	const want = `{"type":"t","data":{"rows":[{"name":"a","kept":1.50},{"name":"b"}],"total":4,"a":true}}`
	if got := string(withoutPhaseCFields("phase_c_mechanism_probe", []byte(answer))); got != want {
		t.Fatalf("removed the wrong paths, or reordered what it kept\nwant: %s\ngot:  %s", want, got)
	}
	reordered := `{"data":{"rows":[{"name":"a","kept":1.50},{"name":"b"}],"total":4,"a":true},"type":"t"}`
	if string(withoutPhaseCFields("phase_c_mechanism_probe", []byte(reordered))) == want {
		t.Fatal("a change of key order outside the removed paths went unseen")
	}
	if unchanged := withoutPhaseCFields("phase_c_unregistered_type", []byte(answer)); string(unchanged) != answer {
		t.Fatalf("a type with no registered path was changed: %s", unchanged)
	}
}
