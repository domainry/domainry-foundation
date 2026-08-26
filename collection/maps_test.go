package collection

import (
	"reflect"
	"testing"
)

type indexedValue struct {
	Key  string
	Name string
}

func TestIndexByAndKeySetBy(t *testing.T) {
	items := []indexedValue{{Key: "b", Name: "first"}, {Key: "a", Name: "only"}, {Key: "b", Name: "last"}}
	indexed := IndexBy(items, func(item indexedValue) string { return item.Key })
	if len(indexed) != 2 || indexed["b"].Name != "last" || indexed["a"].Name != "only" {
		t.Fatalf("indexed = %#v", indexed)
	}
	set := KeySetBy(items, func(item indexedValue) string { return item.Key })
	if len(set) != 2 {
		t.Fatalf("set = %#v", set)
	}
	if indexed := IndexBy(items, (func(indexedValue) string)(nil)); len(indexed) != 0 {
		t.Fatalf("nil selector index = %#v", indexed)
	}
	if set := KeySetBy(items, (func(indexedValue) string)(nil)); len(set) != 0 {
		t.Fatalf("nil selector set = %#v", set)
	}
}

func TestSortedKeysAndValuesBySortedKeys(t *testing.T) {
	values := map[string]int{"b": 2, "a": 1, "c": 3}
	if keys := SortedKeys(values); !reflect.DeepEqual(keys, []string{"a", "b", "c"}) {
		t.Fatalf("keys = %#v", keys)
	}
	if ordered := ValuesBySortedKeys(values); !reflect.DeepEqual(ordered, []int{1, 2, 3}) {
		t.Fatalf("values = %#v", ordered)
	}
	if keys := SortedKeys(map[string]int(nil)); len(keys) != 0 || keys == nil {
		t.Fatalf("nil map keys = %#v", keys)
	}
}
