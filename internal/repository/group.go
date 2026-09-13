package repository

import (
	"time"

	"github.com/google/uuid"
)

// Group is the groups table.
type Group struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name      string    `gorm:"type:varchar(150);uniqueIndex;not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (Group) TableName() string { return "groups" }

// UserGroup is the user_groups join table. The composite primary key keeps a
// user from joining the same group twice; the extra index on group_id serves
// member listings, which cannot use the composite key's leading user_id column.
type UserGroup struct {
	UserID  uuid.UUID `gorm:"type:uuid;primaryKey"`
	GroupID uuid.UUID `gorm:"type:uuid;primaryKey;index"`

	User  User  `gorm:"foreignKey:UserID;references:ID"`
	Group Group `gorm:"foreignKey:GroupID;references:ID"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (UserGroup) TableName() string { return "user_groups" }
