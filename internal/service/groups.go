package service

import (
	"context"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
)

// UpdateGroupInput is a partial update: a nil field is left untouched.
type UpdateGroupInput struct {
	Name *string
}

// CreateGroup validates the name, checks uniqueness and stores the group.
func CreateGroup(ctx context.Context, name string) (*repository.Group, error) {
	if err := validateGroupName(name); err != nil {
		return nil, err
	}

	taken, err := repository.GroupNameTaken(ctx, config.DB, name, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrGroupNameTaken
	}

	group := &repository.Group{ID: uuid.New(), Name: name}
	if err := repository.CreateGroup(ctx, config.DB, group); err != nil {
		return nil, mapWriteError(err)
	}
	return group, nil
}

// UpdateGroup renames a group.
func UpdateGroup(ctx context.Context, id uuid.UUID, in UpdateGroupInput) (*repository.Group, error) {
	group, err := repository.GetGroupByID(ctx, config.DB, id)
	if err != nil {
		return nil, err
	}

	if in.Name != nil {
		if err := validateGroupName(*in.Name); err != nil {
			return nil, err
		}
		taken, err := repository.GroupNameTaken(ctx, config.DB, *in.Name, id)
		if err != nil {
			return nil, err
		}
		if taken {
			return nil, ErrGroupNameTaken
		}
		group.Name = *in.Name
	}

	if err := repository.SaveGroup(ctx, config.DB, group); err != nil {
		return nil, mapWriteError(err)
	}
	return group, nil
}

// DeleteGroup removes a group and its memberships for good.
func DeleteGroup(ctx context.Context, id uuid.UUID) error {
	return repository.DeleteGroup(ctx, config.DB, id)
}

// AddGroupMember makes a user a member of a group. It is idempotent.
func AddGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	if _, err := repository.GetGroupByID(ctx, config.DB, groupID); err != nil {
		return err
	}
	if _, err := repository.GetUserByID(ctx, config.DB, userID); err != nil {
		return err
	}
	return repository.AddUserToGroup(ctx, config.DB, userID, groupID)
}

// RemoveGroupMember drops a membership. Removing one that does not exist is a
// no-op.
func RemoveGroupMember(ctx context.Context, groupID, userID uuid.UUID) error {
	if _, err := repository.GetGroupByID(ctx, config.DB, groupID); err != nil {
		return err
	}
	if _, err := repository.GetUserByID(ctx, config.DB, userID); err != nil {
		return err
	}
	return repository.RemoveUserFromGroup(ctx, config.DB, userID, groupID)
}

// ListGroups returns one page of groups.
func ListGroups(ctx context.Context, limit, offset int) ([]repository.Group, int64, error) {
	return repository.ListGroups(ctx, config.DB, limit, offset)
}

// GetGroup loads a single group by primary key.
func GetGroup(ctx context.Context, id uuid.UUID) (*repository.Group, error) {
	return repository.GetGroupByID(ctx, config.DB, id)
}

// ListGroupUsers returns one page of the members of a group.
func ListGroupUsers(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]repository.User, int64, error) {
	if _, err := repository.GetGroupByID(ctx, config.DB, groupID); err != nil {
		return nil, 0, err
	}
	return repository.ListGroupUsers(ctx, config.DB, groupID, limit, offset)
}

// validateGroupName checks that name is present and fits its column.
func validateGroupName(name string) error {
	if name == "" {
		return &ValidationError{Message: "name is required."}
	}
	if len(name) > maxGroupNameLen {
		return &ValidationError{Message: "name is too long."}
	}
	return nil
}
