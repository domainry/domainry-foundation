package pagination

type NumberedOptions struct {
	DefaultPageSize int
	MaximumPageSize int
}

// Numbered is a normalized one-based page request. It is a small value object;
// constructing it does not require a persistent or shared instance.
type Numbered struct {
	page     int
	pageSize int
}

type Window struct {
	Start    int
	End      int
	HasNext  bool
	NextPage int
}

func NewNumbered(page, requestedPageSize int, options NumberedOptions) Numbered {
	if page < 1 {
		page = 1
	}
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
	return Numbered{page: page, pageSize: pageSize}
}

func (p Numbered) Page() int     { return p.page }
func (p Numbered) PageSize() int { return p.pageSize }

// Bounds calculates a safe half-open [Start, End) window for a collection.
func (p Numbered) Bounds(total int) Window {
	if total < 0 {
		total = 0
	}
	start := (p.page - 1) * p.pageSize
	if start > total {
		start = total
	}
	end := start + p.pageSize
	if end > total {
		end = total
	}
	window := Window{Start: start, End: end, HasNext: end < total}
	if window.HasNext {
		window.NextPage = p.page + 1
	}
	return window
}

func SliceNumbered[T any](page Numbered, items []T) ([]T, Window) {
	window := page.Bounds(len(items))
	return items[window.Start:window.End], window
}
