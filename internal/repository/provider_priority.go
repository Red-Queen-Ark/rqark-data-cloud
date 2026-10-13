package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrProviderPriorityNotFound is returned when no override row matches the
// lookup. It also covers an override that belongs to another viewer, so the
// overrides of one user cannot be read through another.
var ErrProviderPriorityNotFound = errors.New("repository: provider priority not found")

// ProviderPriority is the provider_priorities table: the priority one viewer
// applies to a provider it does not own. Sharing a provider hands every user the
// same credentials, so the fields that pick a model stay with the owner; the
// order in which a user tries the providers it may use is that user's own
// business and lives here. A user that sets no override follows the owner's
// priority, so a row only exists once the viewer asks for one.
type ProviderPriority struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID     uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_provider_priorities_user_id_provider_id,priority:1"`
	ProviderID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_provider_priorities_user_id_provider_id,priority:2"`
	Priority   int       `gorm:"type:integer;not null"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`
	// Provider only exists to tie provider_id to providers.id with a foreign key
	// that drops the override together with the provider. It is never loaded or
	// written through this struct.
	Provider Provider `gorm:"foreignKey:ProviderID;references:ID;constraint:OnDelete:CASCADE"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (ProviderPriority) TableName() string { return "provider_priorities" }

// GetProviderPriority loads one viewer's override for a provider. An override
// that belongs to another viewer is reported as ErrProviderPriorityNotFound.
func GetProviderPriority(ctx context.Context, db *gorm.DB, userID, providerID uuid.UUID) (*ProviderPriority, error) {
	var priority ProviderPriority
	err := db.WithContext(ctx).
		First(&priority, "user_id = ? AND provider_id = ?", userID, providerID).Error
	if err != nil {
		return nil, providerPriorityLookupError(err)
	}
	return &priority, nil
}

// ListProviderPriorities returns every override of userID as a provider id to
// priority map, which is the shape the provider listings merge on. An empty map
// is returned when the user holds none.
func ListProviderPriorities(ctx context.Context, db *gorm.DB, userID uuid.UUID) (map[uuid.UUID]int, error) {
	priorities := []ProviderPriority{}
	err := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Find(&priorities).Error
	if err != nil {
		return nil, err
	}

	byProvider := make(map[uuid.UUID]int, len(priorities))
	for i := range priorities {
		byProvider[priorities[i].ProviderID] = priorities[i].Priority
	}
	return byProvider, nil
}

// UpsertProviderPriority stores the viewer's override for a provider, creating
// the row on the first write and overwriting it afterwards. The pair
// (user_id, provider_id) is unique, so a viewer holds at most one override per
// provider.
func UpsertProviderPriority(ctx context.Context, db *gorm.DB, userID, providerID uuid.UUID, priority int) (*ProviderPriority, error) {
	existing, err := GetProviderPriority(ctx, db, userID, providerID)
	switch {
	case err == nil:
		existing.Priority = priority
		if err := db.WithContext(ctx).Save(existing).Error; err != nil {
			return nil, err
		}
		return existing, nil
	case errors.Is(err, ErrProviderPriorityNotFound):
		created := &ProviderPriority{
			ID:         uuid.New(),
			UserID:     userID,
			ProviderID: providerID,
			Priority:   priority,
		}
		if err := db.WithContext(ctx).Create(created).Error; err != nil {
			return nil, err
		}
		return created, nil
	default:
		return nil, err
	}
}

// providerPriorityLookupError maps GORM's not-found error onto the repository's
// own.
func providerPriorityLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProviderPriorityNotFound
	}
	return err
}
