package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrSopNotFound is returned when no sop row matches the lookup. It also covers
// a row owned by another user, so slugs cannot be enumerated.
var ErrSopNotFound = errors.New("repository: sop not found")

// Sop is the sops table: one standard operating procedure a user wrote, keyed
// by its slug. A row belongs to one user and is never seen by another.
//
// A sop has no agent: it is per-user, so the same procedure is available to
// every agent that user talks to. The slug is the name a message calls it by,
// and the pair (user_id, slug) is unique.
type Sop struct {
	ID     uuid.UUID `gorm:"type:uuid;primaryKey"`
	UserID uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_sops_user_id_slug,priority:1"`
	// Slug is the name the command calls: /slug. The CHECK constraint repeats
	// the whitelist the service applies, so a traversing path cannot be stored
	// even by a writer that skipped the service.
	Slug string `gorm:"type:varchar(64);not null;uniqueIndex:idx_sops_user_id_slug,priority:2;check:chk_sops_slug,slug ~ '^[a-z0-9][a-z0-9-]{0,63}$'"`
	// Title is what the slash panel and the list show. It is derived from the
	// content's first heading when the caller does not name one.
	Title string `gorm:"type:varchar(255);not null;default:''"`
	// Content is capped at 2000 characters by a CHECK constraint, so a row that
	// is too long is rejected by the database as well as by the service.
	Content   string    `gorm:"type:text;not null;check:chk_sops_content_length,char_length(content) BETWEEN 1 AND 2000"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	// User only exists to tie the id to its table with a cascading foreign key.
	// It is never loaded or written through this struct.
	User User `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the table name to the plural the design uses.
func (Sop) TableName() string { return "sops" }

// ListSops returns one page of userID's sops, slug ascending. A non-empty
// cursor resumes strictly after that slug; slug is unique per user, so it is a
// complete position on its own.
func ListSops(ctx context.Context, db *gorm.DB, userID uuid.UUID, cursor string, limit int) ([]Sop, error) {
	query := db.WithContext(ctx).Where("user_id = ?", userID)
	if cursor != "" {
		query = query.Where("slug > ?", cursor)
	}

	sops := []Sop{}
	if err := query.Order("slug ASC").Limit(limit).Find(&sops).Error; err != nil {
		return nil, err
	}
	return sops, nil
}

// GetSop loads one of userID's rows by slug. Another user's row is reported as
// ErrSopNotFound.
func GetSop(ctx context.Context, db *gorm.DB, userID uuid.UUID, slug string) (*Sop, error) {
	var sop Sop
	if err := db.WithContext(ctx).First(&sop, "user_id = ? AND slug = ?", userID, slug).Error; err != nil {
		return nil, sopLookupError(err)
	}
	return &sop, nil
}

// CreateSop inserts a row.
func CreateSop(ctx context.Context, db *gorm.DB, sop *Sop) error {
	return db.WithContext(ctx).Create(sop).Error
}

// UpdateSop rewrites the title and content of one of userID's rows and returns
// the stored row. The slug is part of the identity and never changes.
func UpdateSop(ctx context.Context, db *gorm.DB, userID uuid.UUID, slug, title, content string) (*Sop, error) {
	sop, err := GetSop(ctx, db, userID, slug)
	if err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Model(&Sop{}).Where("id = ?", sop.ID).Updates(map[string]any{"title": title, "content": content}).Error; err != nil {
		return nil, err
	}
	return GetSop(ctx, db, userID, slug)
}

// DeleteSop removes one of userID's rows. A row that does not exist is reported
// as ErrSopNotFound.
func DeleteSop(ctx context.Context, db *gorm.DB, userID uuid.UUID, slug string) error {
	result := db.WithContext(ctx).Where("user_id = ?", userID).Delete(&Sop{}, "slug = ?", slug)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrSopNotFound
	}
	return nil
}

// sopLookupError maps GORM's not-found error onto the repository's own.
func sopLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrSopNotFound
	}
	return err
}
