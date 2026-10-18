package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
)

// sopTestDB opens the shared test database and installs the sop tables on top
// of it.
func sopTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.User{}, &repository.Sop{}); err != nil {
		t.Fatalf("automigrate sops: %v", err)
	}
	return db
}

// testSopOwner inserts a user and removes it, its sops included, afterwards.
func testSopOwner(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "soptest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.Sop{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

// TestSopSlugValidation covers the whitelist: a slug is used as a local file
// name too, so traversal and characters a path treats specially are refused.
func TestSopSlugValidation(t *testing.T) {
	db := sopTestDB(t)
	user := testSopOwner(t, db)

	invalid := []string{"", "../escape", "Trip", "with space", "-leading", "has_underscore", strings.Repeat("a", 65)}
	for _, slug := range invalid {
		_, err := CreateSop(context.Background(), user.ID, CreateSopInput{Slug: slug, Content: "# hi"})
		var validation *ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("slug %q: expected ValidationError, got %v", slug, err)
		}
	}

	if _, err := CreateSop(context.Background(), user.ID, CreateSopInput{Slug: "trip-itinerary", Content: "# Trip itinerary"}); err != nil {
		t.Fatalf("create sop: %v", err)
	}
	_, err := CreateSop(context.Background(), user.ID, CreateSopInput{Slug: "trip-itinerary", Content: "# Again"})
	if !errors.Is(err, ErrSopSlugTaken) {
		t.Fatalf("expected ErrSopSlugTaken, got %v", err)
	}
}

// TestSopContentLimits covers the 2000 character ceiling: the two boundary
// values are stored and one character more is refused rather than truncated.
func TestSopContentLimits(t *testing.T) {
	db := sopTestDB(t)
	user := testSopOwner(t, db)

	ctx := context.Background()
	if _, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "exactly-1999", Content: strings.Repeat("字", 1999)}); err != nil {
		t.Fatalf("1999 characters should be stored: %v", err)
	}
	if _, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "exactly-2000", Content: strings.Repeat("字", 2000)}); err != nil {
		t.Fatalf("2000 characters should be stored: %v", err)
	}
	if _, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "too-long", Content: strings.Repeat("字", 2001)}); err == nil {
		t.Fatal("expected over-long content to be rejected")
	}
	if _, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "empty", Content: "   "}); err == nil {
		t.Fatal("expected empty content to be rejected")
	}
}

// TestSopTitleDerivation covers the title rule a local file follows too: the
// first heading names the sop, and a file without one is named by its slug.
func TestSopTitleDerivation(t *testing.T) {
	db := sopTestDB(t)
	user := testSopOwner(t, db)

	ctx := context.Background()
	heading, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "with-heading", Content: "\n# Trip itinerary\n\nPlan it."})
	if err != nil {
		t.Fatalf("create sop: %v", err)
	}
	if heading.Title != "Trip itinerary" {
		t.Fatalf("title = %q, want the heading", heading.Title)
	}

	plain, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "no-heading", Content: "Plan it day by day."})
	if err != nil {
		t.Fatalf("create sop: %v", err)
	}
	if plain.Title != "no-heading" {
		t.Fatalf("title = %q, want the slug", plain.Title)
	}

	named, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "named", Title: "My own name", Content: "# Heading"})
	if err != nil {
		t.Fatalf("create sop: %v", err)
	}
	if named.Title != "My own name" {
		t.Fatalf("title = %q, want the explicit one", named.Title)
	}
}

// TestSopListCursor covers the keyset page: a cursor resumes strictly after the
// slug it names, and two pages together hold every row once.
func TestSopListCursor(t *testing.T) {
	db := sopTestDB(t)
	user := testSopOwner(t, db)

	ctx := context.Background()
	slugs := []string{"alpha", "bravo", "charlie", "delta", "echo"}
	for _, slug := range slugs {
		if _, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: slug, Content: "# " + slug}); err != nil {
			t.Fatalf("create sop %s: %v", slug, err)
		}
	}

	first, err := ListSops(ctx, user.ID, "", 3)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 3 || first[0].Slug != "alpha" || first[2].Slug != "charlie" {
		t.Fatalf("first page = %v, want alpha through charlie", slugsOf(first))
	}

	second, err := ListSops(ctx, user.ID, first[len(first)-1].Slug, 3)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 2 || second[0].Slug != "delta" || second[1].Slug != "echo" {
		t.Fatalf("second page = %v, want delta and echo", slugsOf(second))
	}
}

// TestSopUserScope covers the per-user boundary: another user's slug is not
// readable, writable or deletable, and the list never carries it.
func TestSopUserScope(t *testing.T) {
	db := sopTestDB(t)
	owner := testSopOwner(t, db)
	other := testSopOwner(t, db)

	ctx := context.Background()
	if _, err := CreateSop(ctx, owner.ID, CreateSopInput{Slug: "secret", Content: "# Secret"}); err != nil {
		t.Fatalf("create sop: %v", err)
	}

	if _, err := GetSop(ctx, other.ID, "secret"); !errors.Is(err, repository.ErrSopNotFound) {
		t.Fatalf("expected ErrSopNotFound on read, got %v", err)
	}
	content := "# Hijacked"
	if _, err := UpdateSop(ctx, other.ID, "secret", UpdateSopInput{Content: &content}); !errors.Is(err, repository.ErrSopNotFound) {
		t.Fatalf("expected ErrSopNotFound on update, got %v", err)
	}
	if err := DeleteSop(ctx, other.ID, "secret"); !errors.Is(err, repository.ErrSopNotFound) {
		t.Fatalf("expected ErrSopNotFound on delete, got %v", err)
	}

	sops, err := ListSops(ctx, other.ID, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sops) != 0 {
		t.Fatalf("other user sees %d sops, want 0", len(sops))
	}
}

// TestSopUpdateKeepsTheOtherField covers the patch rule: a field left out is
// left as stored.
func TestSopUpdateKeepsTheOtherField(t *testing.T) {
	db := sopTestDB(t)
	user := testSopOwner(t, db)

	ctx := context.Background()
	if _, err := CreateSop(ctx, user.ID, CreateSopInput{Slug: "trip", Content: "# Trip\n\nOld body."}); err != nil {
		t.Fatalf("create sop: %v", err)
	}

	content := "# Trip\n\nNew body."
	updated, err := UpdateSop(ctx, user.ID, "trip", UpdateSopInput{Content: &content})
	if err != nil {
		t.Fatalf("update content: %v", err)
	}
	if updated.Content != content || updated.Title != "Trip" {
		t.Fatalf("content-only update changed title to %q", updated.Title)
	}

	title := "Renamed"
	renamed, err := UpdateSop(ctx, user.ID, "trip", UpdateSopInput{Title: &title})
	if err != nil {
		t.Fatalf("update title: %v", err)
	}
	if renamed.Title != title || renamed.Content != content {
		t.Fatalf("title-only update changed content to %q", renamed.Content)
	}
}

// slugsOf lists the slugs of a page, for a failure message.
func slugsOf(sops []repository.Sop) []string {
	slugs := make([]string, 0, len(sops))
	for _, sop := range sops {
		slugs = append(slugs, sop.Slug)
	}
	return slugs
}
