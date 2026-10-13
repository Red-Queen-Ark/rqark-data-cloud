package repository

import (
	"testing"

	"github.com/google/uuid"
)

// TestOrderViewerProvidersSortsByThePriorityThatApplies covers the merge of the
// stored priorities and the viewer's override: an override replaces the value,
// a row the viewer owns keeps its own, and the result is ordered lowest first.
func TestOrderViewerProvidersSortsByThePriorityThatApplies(t *testing.T) {
	viewer := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	ownerLow := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	ownerHigh := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	overridden := uuid.MustParse("00000000-0000-0000-0000-000000000003")

	views := orderViewerProviders([]Provider{
		{ID: ownerHigh, UserID: viewer, Priority: 50},
		{ID: ownerLow, UserID: viewer, Priority: 10},
		{ID: overridden, Priority: 99},
	}, map[uuid.UUID]int{overridden: 1}, viewer)

	want := []uuid.UUID{overridden, ownerLow, ownerHigh}
	if len(views) != len(want) {
		t.Fatalf("order = %v, want %v", viewerIDs(views), want)
	}
	for i := range want {
		if views[i].ID != want[i] {
			t.Fatalf("order = %v, want %v", viewerIDs(views), want)
		}
	}

	if views[0].OverridePriority == nil || *views[0].OverridePriority != 1 {
		t.Errorf("the overridden provider carries override %v, want 1", views[0].OverridePriority)
	}
	if views[1].OverridePriority != nil {
		t.Errorf("a provider with no override carries %v, want nil", views[1].OverridePriority)
	}
	if views[0].EffectivePriority(viewer) != 1 || views[1].EffectivePriority(viewer) != 10 {
		t.Errorf("effective priorities = %d, %d, want 1, 10",
			views[0].EffectivePriority(viewer), views[1].EffectivePriority(viewer))
	}
}

// TestSharedProviderWithoutAnOverrideDoesNotBorrowTheOwnersPriority covers the
// shared row the viewer never ordered: it sorts on UnsetProviderPriority rather
// than on the owner's stored number, so the owner editing that number cannot
// reorder somebody else's list.
func TestSharedProviderWithoutAnOverrideDoesNotBorrowTheOwnersPriority(t *testing.T) {
	viewer := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	owner := uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	own := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	shared := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	views := orderViewerProviders([]Provider{
		{ID: own, UserID: viewer, Priority: 10},
		// The owner ranks it first, which says nothing about the viewer's list.
		{ID: shared, UserID: owner, Priority: 1},
	}, nil, viewer)

	if len(views) != 2 || views[0].ID != shared {
		t.Fatalf("order = %v, want [%s %s]", viewerIDs(views), shared, own)
	}
	if got := views[0].EffectivePriority(viewer); got != UnsetProviderPriority {
		t.Errorf("effective priority of the shared row = %d, want %d", got, UnsetProviderPriority)
	}
	if got := views[1].EffectivePriority(viewer); got != 10 {
		t.Errorf("effective priority of the owned row = %d, want 10", got)
	}
}

// TestOrderViewerProvidersBreaksTiesOnID covers the tie: two providers with the
// same effective priority come out in id order, so the listing is stable.
func TestOrderViewerProvidersBreaksTiesOnID(t *testing.T) {
	viewer := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	views := orderViewerProviders([]Provider{
		{ID: second, UserID: viewer, Priority: 5},
		{ID: first, UserID: viewer, Priority: 5},
	}, nil, viewer)

	if len(views) != 2 || views[0].ID != first || views[1].ID != second {
		t.Errorf("order = %v, want [%s %s]", viewerIDs(views), first, second)
	}
}

// viewerIDs lists the ids of the ordered views, for a readable failure.
func viewerIDs(views []ViewerProvider) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(views))
	for i := range views {
		ids = append(ids, views[i].ID)
	}
	return ids
}
