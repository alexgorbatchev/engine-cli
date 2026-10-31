package playlist

import (
	"fmt"
	"slices"
	"testing"
)

func TestMoveOrderPreservesNativeViewSemantics(t *testing.T) {
	all := []int64{10, 11, 12, 13, 14, 15}
	reversed := []int64{15, 14, 13, 12, 11, 10}
	for _, tt := range []struct {
		name string
		view []int64
		rows []int
		drop int
		desc bool
		want []int64
	}{
		{"down", all, []int{1}, 4, false, []int64{10, 12, 13, 11, 14, 15}},
		{"up", all, []int{4}, 1, false, []int64{10, 14, 11, 12, 13, 15}},
		{"top", all, []int{3}, 0, false, []int64{13, 10, 11, 12, 14, 15}},
		{"end", all, []int{1}, 6, false, []int64{10, 12, 13, 14, 15, 11}},
		{"contiguous", all, []int{0, 1}, 6, false, []int64{12, 13, 14, 15, 10, 11}},
		{"separate runs", all, []int{3, 0}, 5, false, []int64{11, 12, 14, 10, 13, 15}},
		{"multiple top", all, []int{1, 2, 4}, 0, false, []int64{11, 12, 14, 10, 13, 15}},
		{"inside selection", all, []int{1, 2, 3}, 2, false, all},
		{"beside selection", all, []int{1, 2, 3}, 4, false, all},
		{"descending", reversed, []int{0}, 3, true, []int64{10, 11, 12, 15, 13, 14}},
		{"descending top", reversed, []int{5}, 0, true, []int64{11, 12, 13, 14, 15, 10}},
		{"descending runs", reversed, []int{1, 3}, 6, true, []int64{12, 14, 10, 11, 13, 15}},
		{"hidden gap", []int64{10, 11, 14, 15}, []int{3}, 2, false, []int64{10, 11, 15, 12, 13, 14}},
		{"hidden self gap", []int64{10, 11, 14, 15}, []int{2}, 2, false, []int64{10, 11, 14, 12, 13, 15}},
		{"hidden end", []int64{10, 11, 12, 13}, []int{0}, 4, false, []int64{11, 12, 13, 10, 14, 15}},
		{"descending hidden", []int64{15, 14, 11, 10}, []int{3}, 1, true, []int64{11, 12, 13, 14, 10, 15}},
		{"descending hidden middle", []int64{15, 14, 11, 10}, []int{0}, 2, true, []int64{10, 11, 12, 13, 15, 14}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			chain := slices.Clone(all)
			view := slices.Clone(tt.view)
			rows := slices.Clone(tt.rows)
			request := orderMove{view: view, rows: rows, drop: tt.drop, descending: tt.desc}
			got, err := moveOrder(chain, request)
			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("order=%v error=%v, want %v", got, err, tt.want)
			}
			if !slices.Equal(chain, all) || !slices.Equal(view, tt.view) || !slices.Equal(rows, tt.rows) {
				t.Fatal("planning mutated an observed input")
			}
		})
	}
}

func BenchmarkMoveOrderAlternatingSelection(b *testing.B) {
	for _, size := range []int{1024, 4096, 65536} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			chain, move := alternatingOrder(size)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := moveOrder(chain, move); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestMoveOrderLargeAlternatingSelection(t *testing.T) {
	chain, move := alternatingOrder(100000)
	got, err := moveOrder(chain, move)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]int64, 0, len(chain))
	for _, remainder := range []int64{0, 1} {
		for _, id := range chain {
			if id%2 == remainder {
				want = append(want, id)
			}
		}
	}
	if !slices.Equal(got, want) {
		t.Fatal("large alternating selection lost stable membership order")
	}
}

func alternatingOrder(size int) ([]int64, orderMove) {
	chain := make([]int64, size)
	rows := make([]int, 0, (size+1)/2)
	for i := range chain {
		chain[i] = int64(i + 1)
		if i%2 == 0 {
			rows = append(rows, i)
		}
	}
	return chain, orderMove{view: chain, rows: rows, drop: len(chain)}
}

func TestMoveOrderRefusesInvalidOrStaleViews(t *testing.T) {
	for _, tt := range []struct {
		name  string
		chain []int64
		move  orderMove
	}{
		{"missing row", []int64{10, 11}, orderMove{view: []int64{10, 11}, rows: []int{2}, drop: 0}},
		{"negative row", []int64{10, 11}, orderMove{view: []int64{10, 11}, rows: []int{-1}, drop: 0}},
		{"duplicate row", []int64{10, 11}, orderMove{view: []int64{10, 11}, rows: []int{0, 0}, drop: 2}},
		{"missing membership", []int64{10, 11}, orderMove{view: []int64{10, 99}, rows: []int{0}, drop: 2}},
		{"duplicate view", []int64{10, 11}, orderMove{view: []int64{10, 10}, rows: []int{0}, drop: 2}},
		{"non-order sort", []int64{10, 11, 12}, orderMove{view: []int64{11, 10, 12}, rows: []int{0}, drop: 2}},
		{"wrong direction", []int64{10, 11}, orderMove{view: []int64{10, 11}, rows: []int{0}, drop: 2, descending: true}},
		{"negative drop", []int64{10, 11}, orderMove{view: []int64{10, 11}, rows: []int{0}, drop: -1}},
		{"drop past end", []int64{10, 11}, orderMove{view: []int64{10, 11}, rows: []int{0}, drop: 3}},
		{"empty selection", []int64{10, 11}, orderMove{view: []int64{10, 11}, drop: 2}},
		{"duplicate chain", []int64{10, 10}, orderMove{view: []int64{10}, rows: []int{0}, drop: 1}},
		{"zero entity", []int64{0, 10}, orderMove{view: []int64{10}, rows: []int{0}, drop: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := moveOrder(tt.chain, tt.move); err == nil || got != nil {
				t.Fatalf("invalid request produced order=%v error=%v", got, err)
			}
		})
	}
}

func TestMoveOrderAllFilteredSelectionsPreserveMembershipAndVisibleIntent(t *testing.T) {
	chain := []int64{10, 11, 12, 13, 14}
	for shownMask := 1; shownMask < 1<<len(chain); shownMask++ {
		var view []int64
		for i, id := range chain {
			if shownMask&(1<<i) != 0 {
				view = append(view, id)
			}
		}
		for _, descending := range []bool{false, true} {
			shown := slices.Clone(view)
			if descending {
				slices.Reverse(shown)
			}
			for selectedMask := 1; selectedMask < 1<<len(shown); selectedMask++ {
				var rows []int
				var picked []int64
				for i, id := range shown {
					if selectedMask&(1<<i) != 0 {
						rows = append(rows, i)
						picked = append(picked, id)
					}
				}
				for drop := 0; drop <= len(shown); drop++ {
					var before, after []int64
					for i, id := range shown {
						if selectedMask&(1<<i) != 0 {
							continue
						}
						if i < drop {
							before = append(before, id)
						} else {
							after = append(after, id)
						}
					}
					want := append(append(before, picked...), after...)
					move := orderMove{view: shown, rows: rows, drop: drop, descending: descending}
					got, err := moveOrder(chain, move)
					if err != nil {
						t.Fatalf("view=%v rows=%v drop=%d desc=%t: %v", shown, rows, drop, descending, err)
					}
					members := slices.Clone(got)
					slices.Sort(members)
					if !slices.Equal(members, chain) {
						t.Fatalf("changed memberships: %v", got)
					}
					if descending {
						slices.Reverse(got)
					}
					var visible []int64
					for _, id := range got {
						if slices.Contains(shown, id) {
							visible = append(visible, id)
						}
					}
					if !slices.Equal(visible, want) {
						t.Fatalf("view=%v rows=%v drop=%d desc=%t: visible=%v want=%v", shown, rows, drop, descending, visible, want)
					}
				}
			}
		}
	}
}
