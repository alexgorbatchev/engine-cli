package progress

import (
	"context"
	"encoding/json"
	"testing"
)

func TestItemReportsProcessedAndTotal(t *testing.T) {
	var wire []map[string]any
	ctx := WithReporter(context.Background(), func(e Event) {
		encoded, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		var event map[string]any
		if err := json.Unmarshal(encoded, &event); err != nil {
			t.Fatal(err)
		}
		wire = append(wire, event)
	})
	Stage(ctx, "work", 5)
	// Counts pass through a parent range unchanged; only the percentage is rescaled.
	Item(Range(ctx, 0, 50), "work.items", 0, 3)
	Item(ctx, "work.items", 3, 3)
	if _, ok := wire[0]["processed"]; ok {
		t.Fatalf("a phase start invented counts: %v", wire[0])
	}
	for i, want := range [][2]float64{{0, 3}, {3, 3}} {
		event := wire[i+1]
		if event["phase"] != "work.items" || event["processed"] != want[0] || event["total"] != want[1] {
			t.Fatalf("item %d=%v want processed %v of %v", i, event, want[0], want[1])
		}
	}
}
