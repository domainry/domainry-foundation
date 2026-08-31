package worker

import "strings"

// FairOrder returns at most limit items in round-robin key order. Callers
// should fetch a bounded over-scan so a hot tenant cannot monopolize a batch.
func FairOrder[T any](items []T, limit int, key func(T) string) []T {
	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	groups := map[string][]T{}
	order := []string{}
	for _, item := range items {
		group := strings.TrimSpace(key(item))
		if group == "" {
			group = "default"
		}
		if _, exists := groups[group]; !exists {
			order = append(order, group)
		}
		groups[group] = append(groups[group], item)
	}
	out := make([]T, 0, limit)
	for len(out) < limit {
		for _, group := range order {
			values := groups[group]
			if len(values) == 0 {
				continue
			}
			out = append(out, values[0])
			groups[group] = values[1:]
			if len(out) == limit {
				break
			}
		}
	}
	return out
}

func OverscanLimit(limit, factor, maximum int) int {
	if limit <= 0 {
		return 0
	}
	if factor < 1 {
		factor = 1
	}
	value := limit * factor
	if maximum > 0 && value > maximum {
		value = maximum
	}
	return value
}

// QuotaFairOrder reserves bounded shares for priority classes, applies
// round-robin fairness inside each class, then reuses idle quota for remaining
// work. Unselected durable items remain available to the next poll.
func QuotaFairOrder[T any](items []T, limit int, key func(T) string, class func(T) string, order []string, quotas map[string]int) []T {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if limit > len(items) {
		limit = len(items)
	}
	classes := map[string][]T{}
	for _, item := range items {
		classes[class(item)] = append(classes[class(item)], item)
	}
	selected := make([]T, 0, limit)
	remaining := make([]T, 0, len(items))
	seen := map[string]bool{}
	for _, name := range order {
		seen[name] = true
		ordered := FairOrder(classes[name], len(classes[name]), key)
		quota := quotas[name]
		if quota < 0 {
			quota = 0
		}
		if quota > limit-len(selected) {
			quota = limit - len(selected)
		}
		if quota > len(ordered) {
			quota = len(ordered)
		}
		selected = append(selected, ordered[:quota]...)
		remaining = append(remaining, ordered[quota:]...)
	}
	for name, values := range classes {
		if !seen[name] {
			remaining = append(remaining, values...)
		}
	}
	if slots := limit - len(selected); slots > 0 {
		selected = append(selected, FairOrder(remaining, slots, key)...)
	}
	return selected
}
