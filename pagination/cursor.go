package pagination

import (
	"fmt"
	"strings"
)

type CursorOptions struct {
	DefaultPageSize int
	MaximumPageSize int
}

type Cursor struct {
	afterID  string
	pageSize int
}

type CursorNotFoundError struct {
	AfterID string
}

func (e CursorNotFoundError) Error() string {
	return fmt.Sprintf("pagination cursor %q was not found", e.AfterID)
}

type Page[T any] struct {
	Items   []T
	HasNext bool
	NextID  string
}

func NewCursor(afterID string, requestedPageSize int, options CursorOptions) Cursor {
	pageSize := requestedPageSize
	if pageSize < 1 {
		pageSize = options.DefaultPageSize
	}
	if pageSize < 1 {
		pageSize = 1
	}
	if options.MaximumPageSize > 0 && pageSize > options.MaximumPageSize {
		pageSize = options.MaximumPageSize
	}
	return Cursor{afterID: strings.TrimSpace(afterID), pageSize: pageSize}
}

func (c Cursor) AfterID() string {
	return c.afterID
}

func (c Cursor) PageSize() int {
	return c.pageSize
}

// FetchLimit includes one look-ahead row used to determine whether another
// page exists without issuing a second boundary query.
func (c Cursor) FetchLimit() int {
	return c.pageSize + 1
}

// Slice locates the cursor in an already ordered collection and returns one
// page. The item identity must be the final stable key of that ordering.
func Slice[T any](cursor Cursor, items []T, itemID func(T) string) (Page[T], error) {
	start := 0
	if cursor.afterID != "" {
		found := false
		for index, item := range items {
			if strings.TrimSpace(itemID(item)) == cursor.afterID {
				start, found = index+1, true
				break
			}
		}
		if !found {
			return Page[T]{}, CursorNotFoundError{AfterID: cursor.afterID}
		}
	}
	end := start + cursor.FetchLimit()
	if end > len(items) {
		end = len(items)
	}
	return Boundary(cursor, items[start:end], itemID), nil
}

// Boundary trims a database result fetched with FetchLimit and derives its
// continuation cursor from the last returned item.
func Boundary[T any](cursor Cursor, fetched []T, itemID func(T) string) Page[T] {
	if len(fetched) <= cursor.pageSize {
		return Page[T]{Items: fetched}
	}
	items := fetched[:cursor.pageSize]
	nextID := ""
	if len(items) > 0 {
		nextID = strings.TrimSpace(itemID(items[len(items)-1]))
	}
	return Page[T]{Items: items, HasNext: true, NextID: nextID}
}
