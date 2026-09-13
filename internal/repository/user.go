// Package repository holds the GORM models and the database access helpers
// built on top of them.
package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
)

// ErrUserNotFound is returned when no user matches the lookup.
var ErrUserNotFound = errors.New("repository: user not found")

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

// GetUserByID loads a user by primary key, returning ErrUserNotFound when the
// row does not exist.
func GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	var user User
	err := config.DB.WithContext(ctx).First(&user, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, err
	}
	return &user, nil
}
