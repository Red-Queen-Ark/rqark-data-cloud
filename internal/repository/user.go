// Package repository holds the GORM models and the database access helpers
// built on top of them. Every helper takes the database handle it should run
// on, so callers can pass either the shared connection or a transaction.
package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrUserNotFound is returned when no user matches the lookup.
var ErrUserNotFound = errors.New("repository: user not found")

// superuserGuardLockKey is the advisory lock that serializes operations which
// can remove the last active superuser.
const superuserGuardLockKey int64 = 8231004291

// User is the users table.
type User struct {
	ID             uuid.UUID `gorm:"type:uuid;primaryKey"`
	Username       string    `gorm:"type:varchar(150);uniqueIndex;not null"`
	Email          string    `gorm:"type:varchar(255);uniqueIndex;not null"`
	HashedPassword string    `gorm:"type:varchar(255);not null"`
	DisplayName    string    `gorm:"type:varchar(255)"`
	IsActive       bool      `gorm:"not null;default:true"`
	IsStaff        bool      `gorm:"not null;default:false"`
	IsSuperuser    bool      `gorm:"not null;default:false"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (User) TableName() string { return "users" }

// UserFilter narrows a user listing.
type UserFilter struct {
	// IsActive, when set, keeps only the users whose is_active column matches.
	IsActive *bool
	// Query matches username, email or display name, case-insensitively. The
	// term is matched literally: LIKE wildcards in it are escaped.
	Query string
}

// apply adds the filter conditions to db.
func (f UserFilter) apply(db *gorm.DB) *gorm.DB {
	if f.IsActive != nil {
		db = db.Where("is_active = ?", *f.IsActive)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		pattern := "%" + escapeLike(q) + "%"
		db = db.Where(
			`username ILIKE ? ESCAPE '\' OR email ILIKE ? ESCAPE '\' OR display_name ILIKE ? ESCAPE '\'`,
			pattern, pattern, pattern,
		)
	}
	return db
}

// escapeLike neutralizes the LIKE wildcards in s, so that a search term is
// matched literally.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GetUserByID loads a user by primary key.
func GetUserByID(ctx context.Context, db *gorm.DB, id uuid.UUID) (*User, error) {
	var user User
	if err := db.WithContext(ctx).First(&user, "id = ?", id).Error; err != nil {
		return nil, userLookupError(err)
	}
	return &user, nil
}

// GetUserByIDForUpdate is GetUserByID with a row lock, for callers that read
// and then write the row inside one transaction.
func GetUserByIDForUpdate(ctx context.Context, tx *gorm.DB, id uuid.UUID) (*User, error) {
	var user User
	err := tx.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		First(&user, "id = ?", id).Error
	if err != nil {
		return nil, userLookupError(err)
	}
	return &user, nil
}

// GetUserByUsername loads a user by username. The comparison is case-sensitive.
func GetUserByUsername(ctx context.Context, db *gorm.DB, username string) (*User, error) {
	var user User
	if err := db.WithContext(ctx).First(&user, "username = ?", username).Error; err != nil {
		return nil, userLookupError(err)
	}
	return &user, nil
}

// GetUserByEmail loads a user by email. The comparison is case-sensitive.
func GetUserByEmail(ctx context.Context, db *gorm.DB, email string) (*User, error) {
	var user User
	if err := db.WithContext(ctx).First(&user, "email = ?", email).Error; err != nil {
		return nil, userLookupError(err)
	}
	return &user, nil
}

// CreateUser inserts a user.
func CreateUser(ctx context.Context, db *gorm.DB, user *User) error {
	return db.WithContext(ctx).Create(user).Error
}

// SaveUser writes every column of user, including its zero values.
func SaveUser(ctx context.Context, db *gorm.DB, user *User) error {
	return db.WithContext(ctx).Save(user).Error
}

// ListUsers returns one page of users matching filter, ordered newest first,
// together with the total number of matching users.
func ListUsers(ctx context.Context, db *gorm.DB, filter UserFilter, limit, offset int) ([]User, int64, error) {
	base := func() *gorm.DB {
		return filter.apply(db.WithContext(ctx).Model(&User{}))
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []User
	err := base().Order("created_at DESC").Order("id").Limit(limit).Offset(offset).Find(&users).Error
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// CountActiveSuperusers returns how many users are both active and superusers.
func CountActiveSuperusers(ctx context.Context, db *gorm.DB) (int64, error) {
	var count int64
	err := db.WithContext(ctx).
		Model(&User{}).
		Where("is_active = ? AND is_superuser = ?", true, true).
		Count(&count).Error
	return count, err
}

// UsernameTaken reports whether another user already owns username. Users with
// excludeID are ignored, so a user keeping its own username is not a conflict.
func UsernameTaken(ctx context.Context, db *gorm.DB, username string, excludeID uuid.UUID) (bool, error) {
	query := db.WithContext(ctx).Model(&User{}).Where("username = ?", username)
	if excludeID != uuid.Nil {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// EmailTaken reports whether another user already owns email.
func EmailTaken(ctx context.Context, db *gorm.DB, email string, excludeID uuid.UUID) (bool, error) {
	query := db.WithContext(ctx).Model(&User{}).Where("email = ?", email)
	if excludeID != uuid.Nil {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// LockSuperuserGuard serializes the transactions that count active superusers
// before acting on them, so two concurrent requests cannot each see the other
// as a spare. The lock is held until the transaction ends.
func LockSuperuserGuard(ctx context.Context, tx *gorm.DB) error {
	return tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(?)", superuserGuardLockKey).Error
}

// userLookupError maps GORM's not-found error onto the repository's own.
func userLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrUserNotFound
	}
	return err
}
