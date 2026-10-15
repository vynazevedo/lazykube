package gui

import "testing"

func TestNextVerticalPosition(t *testing.T) {
	tests := []struct {
		name       string
		current    VerticalPosition
		dy         int
		height     int
		totalLines int
		want       VerticalPosition
	}{
		{name: "move down inside view", current: VerticalPosition{Cursor: 0, Origin: 0}, dy: 1, height: 5, totalLines: 10, want: VerticalPosition{Cursor: 1, Origin: 0}},
		{name: "move up inside view", current: VerticalPosition{Cursor: 2, Origin: 0}, dy: -1, height: 5, totalLines: 10, want: VerticalPosition{Cursor: 1, Origin: 0}},
		{name: "scroll down at bottom edge", current: VerticalPosition{Cursor: 4, Origin: 0}, dy: 1, height: 5, totalLines: 10, want: VerticalPosition{Cursor: 4, Origin: 1}},
		{name: "scroll up at top edge", current: VerticalPosition{Cursor: 0, Origin: 3}, dy: -1, height: 5, totalLines: 10, want: VerticalPosition{Cursor: 0, Origin: 2}},
		{name: "stay at very top", current: VerticalPosition{Cursor: 0, Origin: 0}, dy: -1, height: 5, totalLines: 10, want: VerticalPosition{Cursor: 0, Origin: 0}},
		{name: "stay at last line", current: VerticalPosition{Cursor: 2, Origin: 7}, dy: 1, height: 5, totalLines: 10, want: VerticalPosition{Cursor: 2, Origin: 7}},
		{name: "empty view", current: VerticalPosition{}, dy: 1, height: 5, totalLines: 0, want: VerticalPosition{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextVerticalPosition(tt.current, tt.dy, tt.height, tt.totalLines)
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
