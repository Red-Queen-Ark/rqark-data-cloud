package service

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/utils"
)

const (
	// maxTodoTitleLen is the width of the todo_items.title column.
	maxTodoTitleLen = 255
	// maxTodoDescriptionLen caps one todo description. The column is text, so
	// the cap only exists to keep one row from being used as bulk storage.
	maxTodoDescriptionLen = 8192
)

// CreateTodoInput is the body accepted by POST /api/todos. done starts false
// and done_at is derived, so neither is accepted here.
type CreateTodoInput struct {
	Title       string
	Description string
	DueAt       *time.Time
	SortOrder   int
}

// UpdateTodoInput is a partial update: a nil field is left untouched, while
// DueAt is utils.Optional so an explicit null clears the due date.
type UpdateTodoInput struct {
	Title       *string
	Description *string
	Done        *bool
	DueAt       utils.Optional[time.Time]
	SortOrder   *int
}

// ListTodos returns every todo of userID. A nil done returns the whole list.
func ListTodos(ctx context.Context, userID uuid.UUID, done *bool) ([]repository.Todo, error) {
	return repository.ListTodos(ctx, config.DB, userID, done)
}

// GetTodo loads one of userID's todos.
func GetTodo(ctx context.Context, userID, id uuid.UUID) (*repository.Todo, error) {
	return repository.GetTodoByID(ctx, config.DB, userID, id)
}

// CreateTodo validates the item and stores it under userID, open (not done) and
// with no completion time.
func CreateTodo(ctx context.Context, userID uuid.UUID, in CreateTodoInput) (*repository.Todo, error) {
	title, err := normalizeTodoTitle(in.Title)
	if err != nil {
		return nil, err
	}
	description, err := normalizeTodoDescription(in.Description)
	if err != nil {
		return nil, err
	}

	todo := &repository.Todo{
		ID:          uuid.New(),
		UserID:      userID,
		Title:       title,
		Description: description,
		DueAt:       in.DueAt,
		SortOrder:   in.SortOrder,
	}
	if err := repository.CreateTodo(ctx, config.DB, todo); err != nil {
		return nil, err
	}
	return todo, nil
}

// UpdateTodo applies in to one of userID's todos. Ticking an item off stamps
// done_at with the current time; unticking it clears the stamp, so the two
// columns cannot drift apart.
func UpdateTodo(ctx context.Context, userID, id uuid.UUID, in UpdateTodoInput) (*repository.Todo, error) {
	todo, err := repository.GetTodoByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}

	if in.Title != nil {
		title, err := normalizeTodoTitle(*in.Title)
		if err != nil {
			return nil, err
		}
		todo.Title = title
	}
	if in.Description != nil {
		description, err := normalizeTodoDescription(*in.Description)
		if err != nil {
			return nil, err
		}
		todo.Description = description
	}
	if in.DueAt.Set {
		todo.DueAt = in.DueAt.Value
	}
	if in.SortOrder != nil {
		todo.SortOrder = *in.SortOrder
	}
	if in.Done != nil {
		todo.Done = *in.Done
		syncTodoDoneAt(todo, time.Now())
	}

	if err := repository.SaveTodo(ctx, config.DB, todo); err != nil {
		return nil, err
	}
	return todo, nil
}

// DeleteTodo removes one of userID's todos.
func DeleteTodo(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteTodo(ctx, config.DB, userID, id)
}

// syncTodoDoneAt keeps done_at consistent with done: a done item that carries
// no stamp gets one, an open item never keeps one. Repeated ticks of an already
// done item keep the first stamp, so the completion time is when it was
// completed rather than when it was last written.
func syncTodoDoneAt(todo *repository.Todo, now time.Time) {
	if !todo.Done {
		todo.DoneAt = nil
		return
	}
	if todo.DoneAt == nil {
		stamp := now
		todo.DoneAt = &stamp
	}
}

// normalizeTodoTitle trims the title, rejects an empty one and enforces the
// column width. NUL characters are dropped: they cannot be stored in text.
func normalizeTodoTitle(title string) (string, error) {
	trimmed := strings.TrimSpace(utils.StripNullChars(title).(string))
	if trimmed == "" {
		return "", &ValidationError{Message: "title is required."}
	}
	if len(trimmed) > maxTodoTitleLen {
		return "", &ValidationError{Message: "title is too long."}
	}
	return trimmed, nil
}

// normalizeTodoDescription drops NUL characters and enforces the size cap.
func normalizeTodoDescription(description string) (string, error) {
	cleaned := utils.StripNullChars(description).(string)
	if len(cleaned) > maxTodoDescriptionLen {
		return "", &ValidationError{Message: "description is too long."}
	}
	return cleaned, nil
}
