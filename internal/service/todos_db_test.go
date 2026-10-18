package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// testTodoDatabase opens the test database and adds the todo table to it.
func testTodoDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	db := testDatabase(t)
	if err := db.AutoMigrate(&repository.Todo{}); err != nil {
		t.Fatalf("automigrate todos: %v", err)
	}
	return db
}

// testTodoOwner inserts a user the tests can hang todos on and removes the user
// and its todos afterwards.
func testTodoOwner(t *testing.T, db *gorm.DB) *repository.User {
	t.Helper()

	id := uuid.New()
	user := &repository.User{
		ID:             id,
		Username:       "todotest-" + id.String(),
		Email:          id.String() + "@example.com",
		HashedPassword: "not-a-hash",
		IsActive:       true,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Cleanup(func() {
		db.Where("user_id = ?", id).Delete(&repository.Todo{})
		db.Delete(&repository.User{}, "id = ?", id)
	})
	return user
}

func TestCreateTodoTrimsTheTitle(t *testing.T) {
	db := testTodoDatabase(t)
	owner := testTodoOwner(t, db)

	todo, err := CreateTodo(context.Background(), owner.ID, CreateTodoInput{Title: "  buy milk\n"})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if todo.Title != "buy milk" {
		t.Errorf("title = %q, want %q", todo.Title, "buy milk")
	}
	if todo.Done {
		t.Error("a new todo was stored as done")
	}
	if todo.DoneAt != nil {
		t.Errorf("done_at = %v on a new todo, want nil", todo.DoneAt)
	}

	stored := reloadTodo(t, db, owner.ID, todo.ID)
	if stored.Title != "buy milk" {
		t.Errorf("stored title = %q, want %q", stored.Title, "buy milk")
	}
}

func TestCreateTodoRejectsBadInput(t *testing.T) {
	db := testTodoDatabase(t)
	owner := testTodoOwner(t, db)

	cases := []struct {
		name string
		in   CreateTodoInput
	}{
		{name: "empty title", in: CreateTodoInput{Title: ""}},
		{name: "whitespace title", in: CreateTodoInput{Title: " \t\n "}},
		{name: "title over the column limit", in: CreateTodoInput{Title: strings.Repeat("a", maxTodoTitleLen+1)}},
		{name: "description over the cap", in: CreateTodoInput{Title: "ok", Description: strings.Repeat("a", maxTodoDescriptionLen+1)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := CreateTodo(context.Background(), owner.ID, tc.in); err == nil {
				t.Fatal("CreateTodo accepted invalid input")
			}

			var count int64
			if err := db.Model(&repository.Todo{}).Where("user_id = ?", owner.ID).Count(&count).Error; err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != 0 {
				t.Errorf("stored %d todos for rejected input, want 0", count)
			}
		})
	}
}

func TestUpdateTodoKeepsDoneAtInSync(t *testing.T) {
	db := testTodoDatabase(t)
	owner := testTodoOwner(t, db)
	ctx := context.Background()

	todo := newTestTodo(t, owner, "water the plants")

	done := true
	ticked, err := UpdateTodo(ctx, owner.ID, todo.ID, UpdateTodoInput{Done: &done})
	if err != nil {
		t.Fatalf("UpdateTodo(done): %v", err)
	}
	if !ticked.Done || ticked.DoneAt == nil {
		t.Fatalf("done = %v, done_at = %v after ticking, want true and a stamp", ticked.Done, ticked.DoneAt)
	}
	first := *ticked.DoneAt

	// A second tick of an already done item must not move the completion time.
	again, err := UpdateTodo(ctx, owner.ID, todo.ID, UpdateTodoInput{Done: &done})
	if err != nil {
		t.Fatalf("UpdateTodo(done) again: %v", err)
	}
	if again.DoneAt == nil || !again.DoneAt.Equal(first) {
		t.Errorf("done_at = %v on a repeat tick, want %v", again.DoneAt, first)
	}

	open := false
	unticked, err := UpdateTodo(ctx, owner.ID, todo.ID, UpdateTodoInput{Done: &open})
	if err != nil {
		t.Fatalf("UpdateTodo(open): %v", err)
	}
	if unticked.Done || unticked.DoneAt != nil {
		t.Errorf("done = %v, done_at = %v after unticking, want false and nil", unticked.Done, unticked.DoneAt)
	}
}

func TestUpdateTodoLeavesAbsentFieldsAlone(t *testing.T) {
	db := testTodoDatabase(t)
	owner := testTodoOwner(t, db)
	ctx := context.Background()

	due := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
	todo, err := CreateTodo(ctx, owner.ID, CreateTodoInput{Title: "renew passport", Description: "keep", DueAt: &due, SortOrder: 3})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	title := "renew passport (urgent)"
	updated, err := UpdateTodo(ctx, owner.ID, todo.ID, UpdateTodoInput{Title: &title})
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}

	if updated.Title != title {
		t.Errorf("title = %q, want %q", updated.Title, title)
	}
	if updated.Description != "keep" {
		t.Errorf("description = %q, want it left alone", updated.Description)
	}
	if updated.DueAt == nil || !updated.DueAt.Equal(due) {
		t.Errorf("due_at = %v, want %v (absent means untouched)", updated.DueAt, due)
	}
	if updated.SortOrder != 3 {
		t.Errorf("sort_order = %d, want 3", updated.SortOrder)
	}

	// An explicit null is not the same as an absent key: it clears the column.
	cleared, err := UpdateTodo(ctx, owner.ID, todo.ID, UpdateTodoInput{DueAt: utils.Optional[time.Time]{Set: true}})
	if err != nil {
		t.Fatalf("UpdateTodo(due_at null): %v", err)
	}
	if cleared.DueAt != nil {
		t.Errorf("due_at = %v after an explicit null, want nil", cleared.DueAt)
	}
}

func TestListTodosOrdersOpenFirstAndFilters(t *testing.T) {
	db := testTodoDatabase(t)
	owner := testTodoOwner(t, db)
	ctx := context.Background()

	second, err := CreateTodo(ctx, owner.ID, CreateTodoInput{Title: "second", SortOrder: 2})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	first, err := CreateTodo(ctx, owner.ID, CreateTodoInput{Title: "first", SortOrder: 1})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	done := true
	if _, err := UpdateTodo(ctx, owner.ID, second.ID, UpdateTodoInput{Done: &done}); err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}

	all, err := ListTodos(ctx, owner.ID, nil)
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListTodos returned %d items, want 2", len(all))
	}
	if all[0].ID != first.ID || all[1].ID != second.ID {
		t.Errorf("order = [%s %s], want open item %s before done item %s", all[0].Title, all[1].Title, first.Title, second.Title)
	}

	open := false
	openOnly, err := ListTodos(ctx, owner.ID, &open)
	if err != nil {
		t.Fatalf("ListTodos(open): %v", err)
	}
	if len(openOnly) != 1 || openOnly[0].ID != first.ID {
		t.Errorf("open list = %v, want only %s", openOnly, first.Title)
	}

	doneOnly, err := ListTodos(ctx, owner.ID, &done)
	if err != nil {
		t.Fatalf("ListTodos(done): %v", err)
	}
	if len(doneOnly) != 1 || doneOnly[0].ID != second.ID {
		t.Errorf("done list = %v, want only %s", doneOnly, second.Title)
	}
}

func TestTodoIsScopedToItsOwner(t *testing.T) {
	db := testTodoDatabase(t)
	owner := testTodoOwner(t, db)
	intruder := testTodoOwner(t, db)
	ctx := context.Background()

	todo := newTestTodo(t, owner, "private")

	if _, err := GetTodo(ctx, intruder.ID, todo.ID); !errors.Is(err, repository.ErrTodoNotFound) {
		t.Errorf("GetTodo by another user = %v, want %v", err, repository.ErrTodoNotFound)
	}
	title := "hijacked"
	if _, err := UpdateTodo(ctx, intruder.ID, todo.ID, UpdateTodoInput{Title: &title}); !errors.Is(err, repository.ErrTodoNotFound) {
		t.Errorf("UpdateTodo by another user = %v, want %v", err, repository.ErrTodoNotFound)
	}
	if err := DeleteTodo(ctx, intruder.ID, todo.ID); !errors.Is(err, repository.ErrTodoNotFound) {
		t.Errorf("DeleteTodo by another user = %v, want %v", err, repository.ErrTodoNotFound)
	}
	if stored := reloadTodo(t, db, owner.ID, todo.ID); stored.Title != "private" {
		t.Errorf("title = %q after a rejected update, want %q", stored.Title, "private")
	}

	mine, err := ListTodos(ctx, intruder.ID, nil)
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	if len(mine) != 0 {
		t.Errorf("another user's list carries %d items, want 0", len(mine))
	}
}

// newTestTodo creates a todo through the service.
func newTestTodo(t *testing.T, owner *repository.User, title string) *repository.Todo {
	t.Helper()

	todo, err := CreateTodo(context.Background(), owner.ID, CreateTodoInput{Title: title})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	return todo
}

// reloadTodo reads a todo back from the database.
func reloadTodo(t *testing.T, db *gorm.DB, ownerID, id uuid.UUID) *repository.Todo {
	t.Helper()

	todo, err := repository.GetTodoByID(context.Background(), db, ownerID, id)
	if err != nil {
		t.Fatalf("GetTodoByID: %v", err)
	}
	return todo
}
