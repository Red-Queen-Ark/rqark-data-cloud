package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrGroupNotFound is returned when no group matches the lookup.
var ErrGroupNotFound = errors.New("repository: group not found")

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

// GetGroupByID loads a group by primary key.
func GetGroupByID(ctx context.Context, db *gorm.DB, id uuid.UUID) (*Group, error) {
	var group Group
	if err := db.WithContext(ctx).First(&group, "id = ?", id).Error; err != nil {
		return nil, groupLookupError(err)
	}
	return &group, nil
}

// CreateGroup inserts a group.
func CreateGroup(ctx context.Context, db *gorm.DB, group *Group) error {
	return db.WithContext(ctx).Create(group).Error
}

// SaveGroup writes every column of group.
func SaveGroup(ctx context.Context, db *gorm.DB, group *Group) error {
	return db.WithContext(ctx).Save(group).Error
}

// ListGroups returns one page of groups ordered by name, together with the
// total number of groups.
func ListGroups(ctx context.Context, db *gorm.DB, limit, offset int) ([]Group, int64, error) {
	var total int64
	if err := db.WithContext(ctx).Model(&Group{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var groups []Group
	err := db.WithContext(ctx).
		Order("name").Order("id").
		Limit(limit).Offset(offset).
		Find(&groups).Error
	if err != nil {
		return nil, 0, err
	}
	return groups, total, nil
}

// DeleteGroup removes a group and its memberships. Both statements run in one
// transaction: the memberships go first so the foreign key never blocks.
func DeleteGroup(ctx context.Context, db *gorm.DB, id uuid.UUID) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", id).Delete(&UserGroup{}).Error; err != nil {
			return err
		}

		result := tx.Delete(&Group{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrGroupNotFound
		}
		return nil
	})
}

// GroupNameTaken reports whether another group already owns name.
func GroupNameTaken(ctx context.Context, db *gorm.DB, name string, excludeID uuid.UUID) (bool, error) {
	query := db.WithContext(ctx).Model(&Group{}).Where("name = ?", name)
	if excludeID != uuid.Nil {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// AddUserToGroup makes userID a member of groupID. Adding the same member twice
// is a no-op.
func AddUserToGroup(ctx context.Context, db *gorm.DB, userID, groupID uuid.UUID) error {
	membership := UserGroup{UserID: userID, GroupID: groupID}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&membership).Error
}

// RemoveUserFromGroup drops the membership of userID in groupID. Removing a
// membership that does not exist is a no-op.
func RemoveUserFromGroup(ctx context.Context, db *gorm.DB, userID, groupID uuid.UUID) error {
	return db.WithContext(ctx).
		Where("user_id = ? AND group_id = ?", userID, groupID).
		Delete(&UserGroup{}).Error
}

// ListGroupUsers returns one page of the members of groupID, ordered by
// username, together with the total number of members.
func ListGroupUsers(ctx context.Context, db *gorm.DB, groupID uuid.UUID, limit, offset int) ([]User, int64, error) {
	base := func() *gorm.DB {
		return db.WithContext(ctx).
			Model(&User{}).
			Joins("JOIN user_groups ON user_groups.user_id = users.id").
			Where("user_groups.group_id = ?", groupID)
	}

	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var users []User
	err := base().Order("users.username").Limit(limit).Offset(offset).Find(&users).Error
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// groupLookupError maps GORM's not-found error onto the repository's own.
func groupLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrGroupNotFound
	}
	return err
}
