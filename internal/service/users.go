package service

import (
	"context"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// Column limits enforced before the database sees the values.
const (
	maxUsernameLen    = 150
	maxEmailLen       = 255
	maxDisplayNameLen = 255
	maxGroupNameLen   = 150
)

// CreateUserInput is the body accepted by POST /api/users and
// POST /api/auth/register.
type CreateUserInput struct {
	Username    string
	Email       string
	Password    string
	DisplayName string
	IsSuperuser bool
}

// Validate applies the field and password policy checks shared by the two
// endpoints that create a user.
func (in CreateUserInput) Validate() error {
	if err := validateUsername(in.Username); err != nil {
		return err
	}
	if err := validateEmail(in.Email); err != nil {
		return err
	}
	if err := validateDisplayName(in.DisplayName); err != nil {
		return err
	}
	return ValidatePassword(in.Password, utils.Attributes{
		Username:    in.Username,
		Email:       in.Email,
		DisplayName: in.DisplayName,
	})
}

// UpdateUserInput is a partial update: a nil field is left untouched.
type UpdateUserInput struct {
	Username    *string
	Email       *string
	DisplayName *string
	IsSuperuser *bool
	IsStaff     *bool
	IsActive    *bool
}

// ValidatePassword returns a PolicyError listing every rule password breaks
// for a user with attrs, or nil when the password is acceptable.
func ValidatePassword(password string, attrs utils.Attributes) error {
	violations := utils.ValidatePassword(password, attrs)
	if len(violations) == 0 {
		return nil
	}
	return &PolicyError{Violations: violations}
}

// CreateUser validates the input, checks uniqueness and stores the new user.
func CreateUser(ctx context.Context, in CreateUserInput) (*repository.User, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	taken, err := repository.UsernameTaken(ctx, config.DB, in.Username, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrUsernameTaken
	}

	taken, err = repository.EmailTaken(ctx, config.DB, in.Email, uuid.Nil)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, ErrEmailTaken
	}

	hashed, err := utils.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}

	user := &repository.User{
		ID:             uuid.New(),
		Username:       in.Username,
		Email:          in.Email,
		HashedPassword: hashed,
		DisplayName:    in.DisplayName,
		IsActive:       true,
		IsStaff:        false,
		IsSuperuser:    in.IsSuperuser,
	}
	if err := repository.CreateUser(ctx, config.DB, user); err != nil {
		return nil, mapWriteError(err)
	}
	return user, nil
}

// Register creates an ordinary user. Self-registration can never hand out
// superuser rights.
func Register(ctx context.Context, in CreateUserInput) (*repository.User, error) {
	in.IsSuperuser = false
	return CreateUser(ctx, in)
}

// UpdateUser applies in to the user named by targetID on behalf of actorID. The
// target row is locked for the whole transaction, and the last-superuser guard
// is taken before counting, so concurrent updates cannot both pass it.
func UpdateUser(ctx context.Context, actorID, targetID uuid.UUID, in UpdateUserInput) (*repository.User, error) {
	var updated *repository.User

	err := config.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := repository.GetUserByIDForUpdate(ctx, tx, targetID)
		if err != nil {
			return err
		}

		if in.Username != nil {
			if err := validateUsername(*in.Username); err != nil {
				return err
			}
			taken, err := repository.UsernameTaken(ctx, tx, *in.Username, targetID)
			if err != nil {
				return err
			}
			if taken {
				return ErrUsernameTaken
			}
		}

		if in.Email != nil {
			if err := validateEmail(*in.Email); err != nil {
				return err
			}
			taken, err := repository.EmailTaken(ctx, tx, *in.Email, targetID)
			if err != nil {
				return err
			}
			if taken {
				return ErrEmailTaken
			}
		}

		if in.DisplayName != nil {
			if err := validateDisplayName(*in.DisplayName); err != nil {
				return err
			}
		}

		if actorID == targetID {
			if in.IsSuperuser != nil && !*in.IsSuperuser {
				return ErrSelfDemote
			}
			if in.IsActive != nil && !*in.IsActive {
				return ErrSelfDeactivate
			}
		}

		demote := in.IsSuperuser != nil && !*in.IsSuperuser
		deactivate := in.IsActive != nil && !*in.IsActive
		if user.IsActive && user.IsSuperuser && (demote || deactivate) {
			if err := repository.LockSuperuserGuard(ctx, tx); err != nil {
				return err
			}
			count, err := repository.CountActiveSuperusers(ctx, tx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastSuperuser
			}
		}

		if in.Username != nil {
			user.Username = *in.Username
		}
		if in.Email != nil {
			user.Email = *in.Email
		}
		if in.DisplayName != nil {
			user.DisplayName = *in.DisplayName
		}
		if in.IsSuperuser != nil {
			user.IsSuperuser = *in.IsSuperuser
		}
		if in.IsStaff != nil {
			user.IsStaff = *in.IsStaff
		}
		if in.IsActive != nil {
			user.IsActive = *in.IsActive
		}

		if err := repository.SaveUser(ctx, tx, user); err != nil {
			return mapWriteError(err)
		}
		updated = user
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteUser deactivates the user named by targetID. It is a soft delete: the
// row and its unique username and email stay in place.
func DeleteUser(ctx context.Context, actorID, targetID uuid.UUID) error {
	return config.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := repository.GetUserByIDForUpdate(ctx, tx, targetID)
		if err != nil {
			return err
		}

		if actorID == targetID {
			return ErrSelfDeactivate
		}

		if user.IsActive && user.IsSuperuser {
			if err := repository.LockSuperuserGuard(ctx, tx); err != nil {
				return err
			}
			count, err := repository.CountActiveSuperusers(ctx, tx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastSuperuser
			}
		}

		user.IsActive = false
		return repository.SaveUser(ctx, tx, user)
	})
}

// ChangeOwnPassword replaces the password of a user that knows its current one.
func ChangeOwnPassword(ctx context.Context, userID uuid.UUID, oldPassword, newPassword string) error {
	user, err := repository.GetUserByID(ctx, config.DB, userID)
	if err != nil {
		return err
	}
	if !utils.VerifyPassword(user.HashedPassword, oldPassword) {
		return ErrOldPassword
	}
	return setPassword(ctx, user, newPassword)
}

// SetPassword replaces the password of a user without asking for the old one.
func SetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	user, err := repository.GetUserByID(ctx, config.DB, userID)
	if err != nil {
		return err
	}
	return setPassword(ctx, user, newPassword)
}

// setPassword validates, hashes and stores a new password. It leaves every
// refresh token alone: changing a password never logs anyone out.
func setPassword(ctx context.Context, user *repository.User, newPassword string) error {
	err := ValidatePassword(newPassword, utils.Attributes{
		Username:    user.Username,
		Email:       user.Email,
		DisplayName: user.DisplayName,
	})
	if err != nil {
		return err
	}

	hashed, err := utils.HashPassword(newPassword)
	if err != nil {
		return err
	}
	user.HashedPassword = hashed
	return repository.SaveUser(ctx, config.DB, user)
}

// ListUsers returns one page of users matching filter.
func ListUsers(ctx context.Context, filter repository.UserFilter, limit, offset int) ([]repository.User, int64, error) {
	return repository.ListUsers(ctx, config.DB, filter, limit, offset)
}

// GetUser loads a single user by primary key.
func GetUser(ctx context.Context, id uuid.UUID) (*repository.User, error) {
	return repository.GetUserByID(ctx, config.DB, id)
}

// validateUsername checks that username is present and fits its column.
func validateUsername(username string) error {
	if username == "" {
		return &ValidationError{Message: "username is required."}
	}
	if len(username) > maxUsernameLen {
		return &ValidationError{Message: "username is too long."}
	}
	return nil
}

// validateEmail checks that email is present and fits its column.
func validateEmail(email string) error {
	if email == "" {
		return &ValidationError{Message: "email is required."}
	}
	if len(email) > maxEmailLen {
		return &ValidationError{Message: "email is too long."}
	}
	return nil
}

// validateDisplayName checks that displayName fits its column.
func validateDisplayName(displayName string) error {
	if len(displayName) > maxDisplayNameLen {
		return &ValidationError{Message: "display_name is too long."}
	}
	return nil
}
