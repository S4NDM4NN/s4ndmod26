package replay

import "testing"

func TestParseHighlights(t *testing.T) {
	meta := map[string]string{
		"highlight_3":     "705 2500 12500",
		"highlight_1":     "240 100 9000",
		"highlight_x":     "1 2 3",        // not a slot
		"highlight_2":     "200 5000",     // too few fields
		"highlight_4":     "200 9000 100", // window ends before it starts
		"highlight_5":     "abc 1 2",      // not a number
		"selectionTarget": "0",
	}
	got := parseHighlights(meta)
	if len(got) != 2 {
		t.Fatalf("want 2 highlights, got %v", got)
	}
	if got[0].Player != 1 || got[0].Score != 240 || got[0].WindowStartMs != 100 || got[0].WindowEndMs != 9000 {
		t.Fatalf("first (ordered by slot): %+v", got[0])
	}
	if got[1].Player != 3 || got[1].Score != 705 || got[1].WindowEndMs != 12500 {
		t.Fatalf("second: %+v", got[1])
	}
	if parseHighlights(map[string]string{}) != nil {
		t.Fatalf("no highlights should be nil")
	}
}
