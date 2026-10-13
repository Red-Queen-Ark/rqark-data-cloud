package repository

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrProviderNotFound is returned when no provider matches the lookup. It also
// covers a lookup that names another user's provider, so provider ids cannot be
// enumerated.
var ErrProviderNotFound = errors.New("repository: provider not found")

// UnsetProviderPriority is the priority a shared provider is served with to a
// viewer that never ordered it. The owner's priority is the owner's own ordering:
// serving it to another user would let one user's edit reorder everybody else's
// list, so a shared provider the viewer has not touched is parked below every
// number a user would pick rather than inheriting one it never chose. Lowest
// first means such a provider leads the list.
const UnsetProviderPriority = -99

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
	// IsPublic marks a provider its owner shares with every other user. Only a
	// superuser may set it, and only the owner may change the row afterwards.
	IsPublic bool `gorm:"not null;default:false;index"`

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

// GetVisibleProviderByID loads a provider the viewer may use by primary key: one
// it owns, or a public one another user shares. Any other provider is reported
// as ErrProviderNotFound, so provider ids cannot be enumerated.
func GetVisibleProviderByID(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Provider, error) {
	var provider Provider
	err := db.WithContext(ctx).
		First(&provider, "id = ? AND (user_id = ? OR is_public = ?)", id, userID, true).Error
	if err != nil {
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

// ViewerProvider is a provider as one viewer sees it: the stored row, plus the
// priority override that viewer set for it, if any. Provider.Priority stays the
// value the owner stored; EffectivePriority is the one this viewer's ordering
// uses.
type ViewerProvider struct {
	Provider
	// OverridePriority is the viewer's own priority for a shared provider, or
	// nil when the viewer set none.
	OverridePriority *int
}

// EffectivePriority is the priority that applies to viewerID, which is the one
// place the ordering rule lives: the viewer's own override when it set one, the
// provider's own priority when the viewer owns the row, and
// UnsetProviderPriority on a shared row the viewer never ordered.
func (v ViewerProvider) EffectivePriority(viewerID uuid.UUID) int {
	if v.OverridePriority != nil {
		return *v.OverridePriority
	}
	if v.Provider.UserID == viewerID {
		return v.Provider.Priority
	}
	return UnsetProviderPriority
}

// ListProviders returns every provider userID may use: the ones it owns plus
// the public providers other users share. The result is ordered by the priority
// that applies to userID, lowest first, which is the order the app tries them
// in.
func ListProviders(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]ViewerProvider, error) {
	providers := []Provider{}
	err := db.WithContext(ctx).
		Where("user_id = ? OR is_public = ?", userID, true).
		Find(&providers).Error
	if err != nil {
		return nil, err
	}

	overrides, err := ListProviderPriorities(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	return orderViewerProviders(providers, overrides, userID), nil
}

// ProviderSummary is the lightweight provider view: the two fields a client
// needs to label a provider, without the credentials the full row carries.
type ProviderSummary struct {
	Name      string
	ModelName string
}

// providerOrderRow is what ListProviderSummaries reads: the two fields the
// summary exposes plus the fields the ordering needs, the owner included.
type providerOrderRow struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Priority  int
	Name      string
	ModelName string
}

// viewerProvider turns a listing row into the viewer's view of it, so the row is
// ordered through the same rule the full listing uses. The result carries only
// the columns the listing selected, so it is good for ordering and for the
// summary fields, and is never a full provider to serve.
func (row providerOrderRow) viewerProvider(viewerID uuid.UUID, overrides map[uuid.UUID]int) ViewerProvider {
	view := ViewerProvider{Provider: Provider{
		ID:        row.ID,
		UserID:    row.UserID,
		Priority:  row.Priority,
		Name:      row.Name,
		ModelName: row.ModelName,
	}}
	if priority, ok := overrides[row.ID]; ok {
		view.OverridePriority = &priority
	}
	return view
}

// ListProviderSummaries returns the name and model_name of every provider
// userID may use, in the same order ListProviders uses. It reads only the
// columns the summary needs plus the ones the ordering needs, so it never loads
// a credential.
func ListProviderSummaries(ctx context.Context, db *gorm.DB, userID uuid.UUID) ([]ProviderSummary, error) {
	rows := []providerOrderRow{}
	err := db.WithContext(ctx).
		Table("providers").
		Select("id", "user_id", "priority", "name", "model_name").
		Where("user_id = ? OR is_public = ?", userID, true).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	overrides, err := ListProviderPriorities(ctx, db, userID)
	if err != nil {
		return nil, err
	}

	views := make([]ViewerProvider, 0, len(rows))
	for i := range rows {
		views = append(views, rows[i].viewerProvider(userID, overrides))
	}
	sort.Slice(views, func(i, j int) bool {
		return lessProvider(views[i].ID, views[i].EffectivePriority(userID),
			views[j].ID, views[j].EffectivePriority(userID))
	})

	summaries := make([]ProviderSummary, 0, len(views))
	for i := range views {
		summaries = append(summaries, ProviderSummary{Name: views[i].Name, ModelName: views[i].ModelName})
	}
	return summaries, nil
}

// orderViewerProviders pairs every provider with the viewer's override and sorts
// the result by the priority that then applies, lowest first.
func orderViewerProviders(providers []Provider, overrides map[uuid.UUID]int, viewerID uuid.UUID) []ViewerProvider {
	views := make([]ViewerProvider, 0, len(providers))
	for i := range providers {
		view := ViewerProvider{Provider: providers[i]}
		if priority, ok := overrides[providers[i].ID]; ok {
			view.OverridePriority = &priority
		}
		views = append(views, view)
	}

	sort.Slice(views, func(i, j int) bool {
		return lessProvider(views[i].ID, views[i].EffectivePriority(viewerID), views[j].ID, views[j].EffectivePriority(viewerID))
	})
	return views
}

// lessProvider orders two providers by the priority that applies to the viewer,
// lowest first, breaking a tie on id so the order is total and stable.
func lessProvider(aID uuid.UUID, aPriority int, bID uuid.UUID, bPriority int) bool {
	if aPriority != bPriority {
		return aPriority < bPriority
	}
	return aID.String() < bID.String()
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
