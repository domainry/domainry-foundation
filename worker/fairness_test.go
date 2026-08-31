package worker

import "testing"

func TestFairOrderPreventsHotWorkspaceMonopoly(t *testing.T) {
	type item struct{ workspace, id string }
	items := []item{{"hot", "1"}, {"hot", "2"}, {"hot", "3"}, {"other", "1"}}
	ordered := FairOrder(items, 3, func(value item) string { return value.workspace })
	if len(ordered) != 3 || ordered[0].workspace != "hot" || ordered[1].workspace != "other" || ordered[2].workspace != "hot" {
		t.Fatalf("unfair order: %#v", ordered)
	}
	if got := OverscanLimit(200, 4, 800); got != 800 {
		t.Fatalf("overscan=%d", got)
	}
}

func TestQuotaFairOrderReservesClassesAndReusesIdleQuota(t *testing.T) {
	type item struct{ workspace, class string }
	items := []item{{"hot", "retry"}, {"hot", "retry"}, {"hot", "retry"}, {"a", "new"}, {"b", "new"}, {"c", "manual"}}
	ordered := QuotaFairOrder(items, 4, func(value item) string { return value.workspace }, func(value item) string { return value.class }, []string{"retry", "manual", "new"}, map[string]int{"retry": 1, "manual": 1, "new": 2})
	counts := map[string]int{}
	for _, value := range ordered {
		counts[value.class]++
	}
	if len(ordered) != 4 || counts["retry"] != 1 || counts["manual"] != 1 || counts["new"] != 2 {
		t.Fatalf("quota ordering=%#v counts=%#v", ordered, counts)
	}
	withoutManual := QuotaFairOrder(items[:5], 4, func(value item) string { return value.workspace }, func(value item) string { return value.class }, []string{"retry", "manual", "new"}, map[string]int{"retry": 1, "manual": 1, "new": 2})
	if len(withoutManual) != 4 {
		t.Fatalf("idle quota was not reused: %#v", withoutManual)
	}
}

func TestFairnessBoundaryInputs(t *testing.T) {
	type boundaryItem struct{ group, class string }
	items := []boundaryItem{{group: "", class: "known"}, {group: "b", class: "extra"}, {group: "b", class: "extra"}}
	ordered := FairOrder(items, 10, func(value boundaryItem) string { return value.group })
	if len(ordered) != len(items) || ordered[0].group != "" {
		t.Fatalf("fair order=%+v", ordered)
	}
	if OverscanLimit(0, 2, 3) != 0 || OverscanLimit(2, 0, 0) != 2 || OverscanLimit(2, 2, 3) != 3 {
		t.Fatal("overscan boundary mismatch")
	}
	if got := QuotaFairOrder(items, 0, func(value boundaryItem) string { return value.group }, func(value boundaryItem) string { return value.class }, nil, nil); got != nil {
		t.Fatalf("zero quota order=%+v", got)
	}
	if got := QuotaFairOrder([]boundaryItem{}, 1, func(value boundaryItem) string { return value.group }, func(value boundaryItem) string { return value.class }, nil, nil); got != nil {
		t.Fatalf("empty quota order=%+v", got)
	}
	got := QuotaFairOrder(items, 3, func(value boundaryItem) string { return value.group }, func(value boundaryItem) string { return value.class }, []string{"known"}, map[string]int{"known": -1})
	if len(got) != 3 {
		t.Fatalf("negative quota/unlisted class order=%+v", got)
	}
	got = QuotaFairOrder(items[:1], 10, func(value boundaryItem) string { return value.group }, func(value boundaryItem) string { return value.class }, []string{"known"}, map[string]int{"known": 10})
	if len(got) != 1 {
		t.Fatalf("bounded quota order=%+v", got)
	}
}
