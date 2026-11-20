package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// providerTestDB opens the shared test database and installs the tables the
// provider tests need on top of it.
func providerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.Provider{}, &repository.ProviderPriority{}); err != nil {
		t.Fatalf("automigrate providers: %v", err)
	}
	return db
}

// testProviderUser inserts a user the provider tests can hang providers on and
// removes the user, its providers and the overrides around them afterwards.
func testProviderUser(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "providertest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		// Overrides this user's providers carry, whichever viewer set them.
		db.Where("provider_id IN (SELECT id FROM providers WHERE user_id = ?)", id).
			Delete(&repository.ProviderPriority{})
		// The user's own overrides on other users' providers.
		db.Where("user_id = ?", id).Delete(&repository.ProviderPriority{})
		db.Where("user_id = ?", id).Delete(&repository.Provider{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

// newTestProvider stores one provider for owner.
func newTestProvider(t *testing.T, db *gorm.DB, owner *repository.User, priority int, isPublic bool) *repository.Provider {
	t.Helper()

	id := uuid.New()
	provider := &repository.Provider{
		ID:        id,
		UserID:    owner.ID,
		Name:      "provider-" + id.String(),
		ModelName: "model-" + id.String(),
		APIKey:    "sk-test",
		APIBase:   "https://example.invalid/v1",
		Priority:  priority,
		IsPublic:  isPublic,
	}
	if err := db.Create(provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	return provider
}

// listedIDs reads the provider ids the service returns for user, in order.
func listedIDs(t *testing.T, user *repository.User) []uuid.UUID {
	t.Helper()

	providers, err := ListProviders(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	ids := make([]uuid.UUID, 0, len(providers))
	for i := range providers {
		ids = append(ids, providers[i].ID)
	}
	return ids
}

// wantIDs fails when got does not hold want, in that order.
func wantIDs(t *testing.T, got, want []uuid.UUID) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("providers = %v, want %v", got, want)
		}
	}
}

// TestProviderPriorityReordersOnlyItsOwner covers the point of the override: it
// changes the order in which the user that set it tries its providers, and the
// provider row itself stays as its owner stored it.
func TestProviderPriorityReordersOnlyItsOwner(t *testing.T) {
	db := providerTestDB(t)
	owner := testProviderUser(t, db)
	viewer := testProviderUser(t, db)

	own := newTestProvider(t, db, viewer, 20, false)
	shared := newTestProvider(t, db, owner, 50, true)

	// The shared row carries no override of the viewer's, so its owner's 50 says
	// nothing here: it sorts unset, ahead of the viewer's own row.
	wantIDs(t, listedIDs(t, viewer), []uuid.UUID{shared.ID, own.ID})

	// Ranking it behind the viewer's own row moves only the viewer's list.
	if _, err := SetProviderPriority(context.Background(), viewer.ID, shared.ID, 30); err != nil {
		t.Fatalf("SetProviderPriority: %v", err)
	}
	wantIDs(t, listedIDs(t, viewer), []uuid.UUID{own.ID, shared.ID})

	stored, err := repository.GetProviderByID(context.Background(), db, owner.ID, shared.ID)
	if err != nil {
		t.Fatalf("GetProviderByID: %v", err)
	}
	if stored.Priority != 50 {
		t.Errorf("stored priority = %d, want the owner's 50 left alone", stored.Priority)
	}
}

// TestProviderPriorityReportsTheEffectiveValue covers the two fields a client
// reads: the priority that applies and the override that produced it.
func TestProviderPriorityReportsTheEffectiveValue(t *testing.T) {
	db := providerTestDB(t)
	owner := testProviderUser(t, db)
	viewer := testProviderUser(t, db)
	shared := newTestProvider(t, db, owner, 50, true)

	view, err := GetProvider(context.Background(), viewer.ID, shared.ID)
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if view.EffectivePriority(viewer.ID) != repository.UnsetProviderPriority {
		t.Errorf("effective priority = %d, want the unset %d", view.EffectivePriority(viewer.ID), repository.UnsetProviderPriority)
	}
	if view.OverridePriority != nil {
		t.Errorf("override = %v, want nil before one is set", view.OverridePriority)
	}

	view, err = SetProviderPriority(context.Background(), viewer.ID, shared.ID, 7)
	if err != nil {
		t.Fatalf("SetProviderPriority: %v", err)
	}
	if view.EffectivePriority(viewer.ID) != 7 {
		t.Errorf("effective priority = %d, want 7", view.EffectivePriority(viewer.ID))
	}
	if view.OverridePriority == nil || *view.OverridePriority != 7 {
		t.Errorf("override = %v, want 7", view.OverridePriority)
	}

	ownerView, err := GetProvider(context.Background(), owner.ID, shared.ID)
	if err != nil {
		t.Fatalf("GetProvider of the owner: %v", err)
	}
	if ownerView.EffectivePriority(owner.ID) != 50 || ownerView.OverridePriority != nil {
		t.Errorf("the owner sees priority %d override %v, want 50 and nil",
			ownerView.EffectivePriority(owner.ID), ownerView.OverridePriority)
	}
}

// TestProviderPriorityRefusesAProviderTheViewerOwns covers the boundary: the
// priority of a provider you own is a field of the provider, not an override.
func TestProviderPriorityRefusesAProviderTheViewerOwns(t *testing.T) {
	db := providerTestDB(t)
	owner := testProviderUser(t, db)
	mine := newTestProvider(t, db, owner, 1, true)

	var validation *ValidationError
	if _, err := SetProviderPriority(context.Background(), owner.ID, mine.ID, 3); !errors.As(err, &validation) {
		t.Errorf("SetProviderPriority on an owned provider = %v, want a ValidationError", err)
	}
}

// TestProviderPriorityHidesAProviderTheViewerCannotUse covers the 404: an id the
// viewer may not use is refused rather than acknowledged.
func TestProviderPriorityHidesAProviderTheViewerCannotUse(t *testing.T) {
	db := providerTestDB(t)
	owner := testProviderUser(t, db)
	viewer := testProviderUser(t, db)
	private := newTestProvider(t, db, owner, 1, false)

	if _, err := SetProviderPriority(context.Background(), viewer.ID, private.ID, 1); !errors.Is(err, repository.ErrProviderNotFound) {
		t.Errorf("SetProviderPriority on an invisible provider = %v, want %v", err, repository.ErrProviderNotFound)
	}
}

// TestUnsharingAProviderHidesItAndKeepsTheOverride covers the owner's side of
// the boundary: withdrawing the share takes the provider off every other user's
// list, and the override waits for a share to come back rather than being lost.
func TestUnsharingAProviderHidesItAndKeepsTheOverride(t *testing.T) {
	db := providerTestDB(t)
	owner := testProviderUser(t, db)
	viewer := testProviderUser(t, db)
	shared := newTestProvider(t, db, owner, 50, true)

	if _, err := SetProviderPriority(context.Background(), viewer.ID, shared.ID, 5); err != nil {
		t.Fatalf("SetProviderPriority: %v", err)
	}

	off := false
	if _, err := UpdateProvider(context.Background(), owner.ID, true, shared.ID, UpdateProviderInput{IsPublic: &off}); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	if ids := listedIDs(t, viewer); len(ids) != 0 {
		t.Errorf("the viewer still sees %v after the share was withdrawn", ids)
	}
	if _, err := GetProvider(context.Background(), viewer.ID, shared.ID); !errors.Is(err, repository.ErrProviderNotFound) {
		t.Errorf("GetProvider after the share was withdrawn = %v, want %v", err, repository.ErrProviderNotFound)
	}
	if _, err := repository.GetProviderPriority(context.Background(), db, viewer.ID, shared.ID); err != nil {
		t.Errorf("the override did not survive the withdrawal: %v", err)
	}

	on := true
	if _, err := UpdateProvider(context.Background(), owner.ID, true, shared.ID, UpdateProviderInput{IsPublic: &on}); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	view, err := GetProvider(context.Background(), viewer.ID, shared.ID)
	if err != nil {
		t.Fatalf("GetProvider after the share came back: %v", err)
	}
	if view.EffectivePriority(viewer.ID) != 5 {
		t.Errorf("effective priority = %d, want the override 5 back", view.EffectivePriority(viewer.ID))
	}
}

// TestProviderVendorAndHeadersRoundTrip covers the two columns a client fills
// in by hand: they are stored, read back unchanged, and an empty value is stored
// as NULL rather than as an empty string or an empty object.
func TestProviderVendorAndHeadersRoundTrip(t *testing.T) {
	db := providerTestDB(t)
	owner := testProviderUser(t, db)
	ctx := context.Background()

	created, err := CreateProvider(ctx, owner.ID, false, CreateProviderInput{
		ModelName: "kimi-k3",
		APIKey:    "sk-test",
		APIBase:   "https://api.moonshot.cn/v1",
		Priority:  ptr(1),
		Vendor:    ptr("moonshot"),
		Headers:   map[string]string{"X-Api-Version": "2024-06-01"},
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	if created.Vendor == nil || *created.Vendor != "moonshot" {
		t.Errorf("vendor = %v, want moonshot", created.Vendor)
	}
	if created.Headers["X-Api-Version"] != "2024-06-01" {
		t.Errorf("headers = %v, want X-Api-Version kept", created.Headers)
	}

	blank := ""
	empty := map[string]string{}
	if _, err := UpdateProvider(ctx, owner.ID, false, created.ID, UpdateProviderInput{
		Vendor:  utils.Optional[string]{Set: true, Value: &blank},
		Headers: utils.Optional[map[string]string]{Set: true, Value: &empty},
	}); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	stored, err := repository.GetProviderByID(ctx, db, owner.ID, created.ID)
	if err != nil {
		t.Fatalf("GetProviderByID: %v", err)
	}
	if stored.Vendor != nil {
		t.Errorf("vendor = %v, want nil after an empty label", stored.Vendor)
	}
	if stored.Headers != nil {
		t.Errorf("headers = %v, want nil after an empty set", stored.Headers)
	}
}

// TestProviderHeadersAreHeldToWhatNetHTTPAccepts covers the field check on the
// create path, so a header the proxy could not send is refused at write time.
func TestProviderHeadersAreHeldToWhatNetHTTPAccepts(t *testing.T) {
	_, err := CreateProvider(context.Background(), uuid.New(), false, CreateProviderInput{
		ModelName: "kimi-k3",
		APIKey:    "sk-test",
		APIBase:   "https://api.moonshot.cn/v1",
		Priority:  ptr(1),
		Headers:   map[string]string{"X-Api": "a\nb"},
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("CreateProvider with a header line break = %v, want a ValidationError", err)
	}
}
