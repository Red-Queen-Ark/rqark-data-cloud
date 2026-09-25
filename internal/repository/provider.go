package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrProviderNotFound is returned when no provider matches the lookup. It also
// covers a lookup that names another user's provider, so provider ids cannot be
// enumerated.
var ErrProviderNotFound = errors.New("repository: provider not found")

// Provider is the providers table. Every nullable model parameter is a pointer,
// so a column the client never set stays NULL and is read back as null rather
// than as 0 or false.
type Provider struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID           uuid.UUID `gorm:"type:uuid;not null;index"`
	Name             string    `gorm:"type:varchar(255);not null;default:''"`
	ModelName        string    `gorm:"type:varchar(255);not null"`
	APIKey           string    `gorm:"type:varchar(255);not null"`
	APIBase          string    `gorm:"type:varchar(255);not null"`
	Priority         int       `gorm:"type:integer;not null"`
	ReasoningEffort  *string   `gorm:"type:varchar(16)"`
	Thinking         *string   `gorm:"type:varchar(16)"`
	Temperature      *float64  `gorm:"type:double precision"`
	TopP             *float64  `gorm:"type:double precision"`
	Seed             *int      `gorm:"type:integer"`
	FrequencyPenalty *float64  `gorm:"type:double precision"`
	PresencePenalty  *float64  `gorm:"type:double precision"`
	Combine          bool      `gorm:"not null;default:false"`

	// User only exists to tie user_id to users.id with a foreign key. It is
	// never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (Provider) TableName() string { return "providers" }

// GetProviderByID loads one of userID's providers by primary key. Another
// user's provider is reported as ErrProviderNotFound.
func GetProviderByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Provider, error) {
	var provider Provider
	if err := db.WithContext(ctx).First(&provider, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, providerLookupError(err)
	}
	return &provider, nil
}

// CreateProvider inserts a provider.
func CreateProvider(ctx context.Context, db *gorm.DB, provider *Provider) error {
	return db.WithContext(ctx).Create(provider).Error
}

// SaveProvider writes every column of provider.
func SaveProvider(ctx context.Context, db *gorm.DB, provider *Provider) error {
	return db.WithContext(ctx).Save(provider).Error
}

// ListProviders returns all of userID's providers, lowest priority first, which
// is the order the app tries them in.
func ListProviders(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]Provider, error) {
	var providers []Provider
	err := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("priority").Order("id").
		Find(&providers).Error
	if err != nil {
		return nil, err
	}
	return providers, nil
}

// DeleteProvider removes one of userID's providers.
func DeleteProvider(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) error {
	result := db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&Provider{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrProviderNotFound
	}
	return nil
}

// providerLookupError maps GORM's not-found error onto the repository's own.
func providerLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrProviderNotFound
	}
	return err
}
