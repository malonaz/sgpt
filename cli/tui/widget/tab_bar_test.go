package widget

import "testing"

func TestVisibleWindow(t *testing.T) {
	for _, tc := range []struct {
		name           string
		widths         []int
		active, width  int
		wantLo, wantHi int
	}{
		{name: "everything fits", widths: []int{5, 5, 5}, active: 1, width: 17, wantLo: 0, wantHi: 3},
		// No right marker past the last tab: three tabs fit.
		{name: "reaching the end drops a marker", widths: []int{5, 5, 5, 5}, active: 3, width: 19, wantLo: 1, wantHi: 4},
		{name: "grows around the active tab", widths: []int{5, 5, 5, 5, 5}, active: 2, width: 21, wantLo: 1, wantHi: 4},
		{name: "active tab wider than the bar", widths: []int{5, 30, 5}, active: 1, width: 10, wantLo: 1, wantHi: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lo, hi := visibleWindow(tc.widths, tc.active, tc.width)
			if lo != tc.wantLo || hi != tc.wantHi {
				t.Errorf("visibleWindow(%v, %d, %d) = [%d, %d), want [%d, %d)", tc.widths, tc.active, tc.width, lo, hi, tc.wantLo, tc.wantHi)
			}
		})
	}
}
