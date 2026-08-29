package pagination

import (
	"errors"
	"testing"
)

type pageItem struct{ id string }

func pageItemID(item pageItem) string { return item.id }

func TestCursorNormalizesRequestAndProvidesLookAheadLimit(t *testing.T) {
	cursor := NewCursor(" item-1 ", 999, CursorOptions{DefaultPageSize: 20, MaximumPageSize: 200})
	if cursor.AfterID() != "item-1" || cursor.PageSize() != 200 || cursor.FetchLimit() != 201 {
		t.Fatalf("after=%q size=%d limit=%d", cursor.AfterID(), cursor.PageSize(), cursor.FetchLimit())
	}

	defaulted := NewCursor("", 0, CursorOptions{DefaultPageSize: 20, MaximumPageSize: 200})
	if defaulted.PageSize() != 20 {
		t.Fatalf("default size=%d", defaulted.PageSize())
	}
}

func TestSliceLocatesCursorAndComputesBoundary(t *testing.T) {
	cursor := NewCursor("item-1", 1, CursorOptions{DefaultPageSize: 20, MaximumPageSize: 200})
	page, err := Slice(cursor, []pageItem{{"item-1"}, {"item-2"}, {"item-3"}}, pageItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].id != "item-2" || !page.HasNext || page.NextID != "item-2" {
		t.Fatalf("page=%+v", page)
	}
}

func TestSliceRejectsUnknownCursor(t *testing.T) {
	_, err := Slice(NewCursor("missing", 20, CursorOptions{DefaultPageSize: 20}), []pageItem{{"item-1"}}, pageItemID)
	var target CursorNotFoundError
	if !errors.As(err, &target) || target.AfterID != "missing" {
		t.Fatalf("err=%v", err)
	}
}

func TestBoundaryUsesExtraFetchedItem(t *testing.T) {
	cursor := NewCursor("", 2, CursorOptions{DefaultPageSize: 20, MaximumPageSize: 200})
	page := Boundary(cursor, []pageItem{{"item-1"}, {"item-2"}, {"item-3"}}, pageItemID)
	if len(page.Items) != 2 || !page.HasNext || page.NextID != "item-2" {
		t.Fatalf("page=%+v", page)
	}

	last := Boundary(cursor, []pageItem{{"item-1"}, {"item-2"}}, pageItemID)
	if last.HasNext || last.NextID != "" || len(last.Items) != 2 {
		t.Fatalf("last page=%+v", last)
	}
}
