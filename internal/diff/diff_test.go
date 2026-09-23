package diff

import "testing"

func TestIntersects(t *testing.T) {
	ranges := []Range{{5, 7}, {20, 22}}
	cases := []struct {
		start, end int
		want       bool
	}{
		{4, 4, false},
		{5, 5, true},
		{7, 19, true}, // touches end of first range
		{8, 19, false},
		{21, 30, true},
		{30, 40, false},
		{1, 100, true},
	}
	for _, c := range cases {
		if got := Intersects(ranges, c.start, c.end); got != c.want {
			t.Errorf("Intersects(%d,%d) = %v, want %v", c.start, c.end, got, c.want)
		}
	}
	if Intersects(nil, 1, 5) {
		t.Error("no ranges must never intersect")
	}
}

func TestLen(t *testing.T) {
	cases := []struct {
		ranges []Range
		want   int
	}{
		{nil, 0},
		{[]Range{{1, 1}}, 1},
		{[]Range{{5, 7}, {20, 22}}, 6},
	}
	for _, c := range cases {
		if got := Len(c.ranges); got != c.want {
			t.Errorf("Len(%v) = %d, want %d", c.ranges, got, c.want)
		}
	}
}
