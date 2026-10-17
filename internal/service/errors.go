// Package service holds the business rules that sit between the HTTP handlers
// and the repository: field and password validation, uniqueness, the
// self-protection and last-superuser rules, and the token flows.
package service

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// Errors the handlers map onto HTTP codes. Anything else is an internal error.
var (
	// ErrUsernameTaken is returned when a write would duplicate a username.
	ErrUsernameTaken = errors.New("service: username already taken")
	// ErrEmailTaken is returned when a write would duplicate an email.
	ErrEmailTaken = errors.New("service: email already taken")
	// ErrGroupNameTaken is returned when a write would duplicate a group name.
	ErrGroupNameTaken = errors.New("service: group name already taken")
	// ErrAgentNameTaken is returned when a write would duplicate the name of an
	// agent already owned by the same user.
	ErrAgentNameTaken = errors.New("service: agent name already taken")
	// ErrTaskNameTaken is returned when a write would duplicate the name of a
	// task already owned by the same user.
	ErrTaskNameTaken = errors.New("service: task name already taken")
	// ErrTaskAgentAmbiguous is returned when a task names an agent that more
	// than one agent the user may use carries, so the name alone does not say
	// which one to run.
	ErrTaskAgentAmbiguous = errors.New("service: agent name is ambiguous")
	// ErrTaskProviderAmbiguous is returned when a task names a provider that
	// more than one provider the user may use carries.
	ErrTaskProviderAmbiguous = errors.New("service: provider name is ambiguous")
	// ErrTaskRunAlreadyFinal is returned when a finalize call reaches a run that
	// is no longer running, so its result was already recorded by someone else.
	ErrTaskRunAlreadyFinal = errors.New("service: task run is already finalized")
	// ErrSessionConflict is returned when a create would reuse the id of a
	// session that already exists. The app owns session ids, so a repeat means
	// the app and the cloud disagree about a session rather than a fresh one.
	ErrSessionConflict = errors.New("service: session already exists")
	// ErrInvalidCredentials is returned for every failed login, whatever the
	// cause.
	ErrInvalidCredentials = errors.New("service: invalid credentials")
	// ErrInvalidToken is returned for a reset or refresh token that is unknown,
	// expired, already consumed or owned by a user that is gone.
	ErrInvalidToken = errors.New("service: invalid token")
	// ErrOldPassword is returned when the current password does not match.
	ErrOldPassword = errors.New("service: current password is incorrect")
	// ErrLastSuperuser is returned when an operation would leave no active
	// superuser behind.
	ErrLastSuperuser = errors.New("service: would remove the last active superuser")
	// ErrSelfDemote is returned when a caller tries to drop its own superuser
	// rights.
	ErrSelfDemote = errors.New("service: cannot drop your own superuser rights")
	// ErrSelfDeactivate is returned when a caller tries to deactivate itself.
	ErrSelfDeactivate = errors.New("service: cannot deactivate your own account")
	// ErrProviderPublicForbidden is returned when a caller that is not a
	// superuser tries to share one of its providers.
	ErrProviderPublicForbidden = errors.New("service: only a superuser can share a provider")
	// ErrAgentPublicForbidden is returned when a caller that is not a
	// superuser tries to share one of its agents.
	ErrAgentPublicForbidden = errors.New("service: only a superuser can share an agent")
)

// uniqueViolation is the PostgreSQL SQLSTATE for a unique index violation.
const uniqueViolation = "23505"

// ValidationError is a malformed request: a missing field, or a value that is
// too long for its column.
type ValidationError struct {
	Message string
}

// Error returns the message shown to the client.
func (e *ValidationError) Error() string { return e.Message }

// PolicyError carries every password policy violation of a rejected password.
type PolicyError struct {
	Violations []utils.Violation
}

// Error joins the violation messages into one line.
func (e *PolicyError) Error() string {
	messages := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		messages = append(messages, violation.Message)
	}
	return strings.Join(messages, " ")
}

// mapWriteError turns the unique index violations raised by PostgreSQL into the
// sentinel errors above. Constraint names are the ones AutoMigrate creates.
func mapWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		return err
	}

	switch pgErr.ConstraintName {
	case "idx_users_username":
		return ErrUsernameTaken
	case "idx_users_email":
		return ErrEmailTaken
	case "idx_groups_name":
		return ErrGroupNameTaken
	case "idx_agents_user_id_name":
		return ErrAgentNameTaken
	case "idx_tasks_user_id_name":
		return ErrTaskNameTaken
	case "sessions_pkey":
		return ErrSessionConflict
	default:
		return err
	}
}
