package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrTodoNotFound is returned when no todo matches the lookup. It also covers a
// lookup that names another user's todo, so todo ids cannot be enumerated.
var ErrTodoNotFound = errors.New("repository: todo not found")

// Todo is the todo_items table: one todo item of one user. The list is per user
// and flat, with no nesting and no shared rows, so every query carries the
// owner.
//
// done_at is derived rather than stored state the caller controls: the service
// writes it when done turns true and clears it when done turns false, so a row
// can never be done with no timestamp or open with one.
type Todo struct {
	ID uuid.UUID `gorm:"type:uuid;primaryKey"`
	// UserID leads the three composite indexes the list needs: the open/done
	// filter, the due date ordering and the manual ordering. No standalone index
	// is added on top of them: each of those already serves a lookup by owner,
	// which is what the users foreign key needs too.
	UserID      uuid.UUID  `gorm:"type:uuid;not null;index:idx_todo_items_user_id_done,priority:1;index:idx_todo_items_user_id_due_at,priority:1;index:idx_todo_items_user_id_sort_order,priority:1"`
	Title       string     `gorm:"type:varchar(255);not null"`
	Description string     `gorm:"type:text;not null;default:''"`
	Done        bool       `gorm:"not null;default:false;index:idx_todo_items_user_id_done,priority:2"`
	DoneAt      *time.Time `gorm:"type:timestamptz"`
	DueAt       *time.Time `gorm:"type:timestamptz;index:idx_todo_items_user_id_due_at,priority:2"`
	SortOrder   int        `gorm:"not null;default:0;index:idx_todo_items_user_id_sort_order,priority:2"`
	CreatedAt   time.Time  `gorm:"not null"`
	UpdatedAt   time.Time  `gorm:"not null"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (Todo) TableName() string { return "todo_items" }

// GetTodoByID loads one of userID's todos by primary key. Another user's todo
// is reported as ErrTodoNotFound.
func GetTodoByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Todo, error) {
	var todo Todo
	if err := db.WithContext(ctx).First(&todo, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, todoLookupError(err)
	}
	return &todo, nil
}

// CreateTodo inserts a todo.
func CreateTodo(ctx context.Context, db *gorm.DB, todo *Todo) error {
	return db.WithContext(ctx).Create(todo).Error
}

// SaveTodo writes every column of todo.
func SaveTodo(ctx context.Context, db *gorm.DB, todo *Todo) error {
	return db.WithContext(ctx).Save(todo).Error
}

// ListTodos returns every todo of userID, open ones first, then by sort_order
// and creation time. A non-nil done keeps only the matching half of the list.
// The list is not paginated: it is one user's own todo list, which the app
// wants whole, the same way it reads its config.
func ListTodos(ctx context.Context, db *gorm.DB, userID uuid.UUID, done *bool) ([]Todo, error) {
	query := db.WithContext(ctx).Where("user_id = ?", userID)
	if done != nil {
		query = query.Where("done = ?", *done)
	}

	todos := []Todo{}
	err := query.
		Order("done").Order("sort_order").Order("created_at").Order("id").
		Find(&todos).Error
	if err != nil {
		return nil, err
	}
	return todos, nil
}

// DeleteTodo removes one of userID's todos.
func DeleteTodo(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&Todo{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTodoNotFound
	}
	return nil
}

// todoLookupError maps GORM's not-found error onto the repository's own.
func todoLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrTodoNotFound
	}
	return err
}
