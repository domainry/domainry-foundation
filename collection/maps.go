package collection

import (
	"cmp"
	"maps"
	"slices"
)

// IndexBy converts a list into a map using the key selected for each item.
// When keys repeat, the last item wins.
func IndexBy[T any, K comparable](items []T, key func(T) K) map[K]T {
	result := make(map[K]T, len(items))
	if key == nil {
		return result
	}
	for _, item := range items {
		result[key(item)] = item
	}
	return result
}

// KeySetBy converts a list into a set using the key selected for each item.
func KeySetBy[T any, K comparable](items []T, key func(T) K) map[K]struct{} {
	result := make(map[K]struct{}, len(items))
	if key == nil {
		return result
	}
	for _, item := range items {
		result[key(item)] = struct{}{}
	}
	return result
}

// SortedKeys returns map keys in deterministic ascending order.
func SortedKeys[K cmp.Ordered, V any](values map[K]V) []K {
	keys := slices.Sorted(maps.Keys(values))
	if keys == nil {
		return []K{}
	}
	return keys
}

// ValuesBySortedKeys returns map values ordered by their ascending keys.
func ValuesBySortedKeys[K cmp.Ordered, V any](values map[K]V) []V {
	keys := SortedKeys(values)
	result := make([]V, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}
