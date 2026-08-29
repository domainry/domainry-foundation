package pagination

import "testing"

func TestNumberedNormalizesAndBounds(t *testing.T) {
	page := NewNumbered(0, 0, NumberedOptions{DefaultPageSize: 2, MaximumPageSize: 3})
	items, window := SliceNumbered(page, []int{1, 2, 3})
	if page.Page() != 1 || page.PageSize() != 2 || len(items) != 2 || !window.HasNext || window.NextPage != 2 {
		t.Fatalf("unexpected first page: page=%+v items=%v window=%+v", page, items, window)
	}
}

func TestNumberedCapsSizeAndClampsPastEnd(t *testing.T) {
	page := NewNumbered(4, 99, NumberedOptions{DefaultPageSize: 2, MaximumPageSize: 3})
	items, window := SliceNumbered(page, []int{1, 2, 3, 4})
	if page.PageSize() != 3 || len(items) != 0 || window.Start != 4 || window.End != 4 || window.HasNext || window.NextPage != 0 {
		t.Fatalf("unexpected terminal page: page=%+v items=%v window=%+v", page, items, window)
	}
}
