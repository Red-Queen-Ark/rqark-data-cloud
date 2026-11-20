package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The capability tags a provider model may carry. A tag says what the model can
// be used for, so a caller can pick a model without knowing the upstream name.
const (
	ProviderModelCapabilityLLM       = "llm"
	ProviderModelCapabilityEmbedding = "embedding"
	ProviderModelCapabilityRerank    = "rerank"
	ProviderModelCapabilityTTS       = "tts"
	ProviderModelCapabilityASR       = "asr"
	ProviderModelCapabilityVLM       = "vlm"
	ProviderModelCapabilityOCR       = "ocr"
)

// ProviderModelBackfillKey is the system_configs key that records a completed
// provider_models backfill, so a second run is a no-op.
const ProviderModelBackfillKey = "provider_models_backfill"

// ProviderModelParams is the sampling configuration of one model, stored in
// provider_models.params. Every field is optional: a value the owner never set
// is left out of the JSON rather than written as zero, so the upstream provider
// sees it unset. combine keeps its value even when false, because it selects
// whether the sampling parameters are sent at all.
type ProviderModelParams struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	Seed             *int     `json:"seed,omitempty"`
	ReasoningEffort  *string  `json:"reasoning_effort,omitempty"`
	Thinking         *string  `json:"thinking,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	Combine          bool     `json:"combine"`
}

// ProviderModel is the provider_models table: one model an upstream provider
// serves, under one credential. A provider row carries the endpoint and the
// credential; the models it can serve live here, so one credential can expose
// several models without copying the key per model.
type ProviderModel struct {
	ID         uuid.UUID `gorm:"type:uuid;primaryKey"`
	ProviderID uuid.UUID `gorm:"type:uuid;not null;index;uniqueIndex:idx_provider_models_provider_id_model_id,priority:1"`
	// ModelID is the name the upstream API expects. It is unique per provider,
	// so a provider serves each upstream model at most once.
	ModelID string `gorm:"type:varchar(255);not null;uniqueIndex:idx_provider_models_provider_id_model_id,priority:2"`
	// DisplayName is what a client shows. It defaults to ModelID when the owner
	// gives none; the column is filled at write time because a database default
	// cannot read another column.
	DisplayName string `gorm:"type:varchar(255);not null;default:''"`
	// Capabilities is the subset of the capability tags this model serves.
	Capabilities  []string             `gorm:"serializer:json;type:jsonb;not null;default:'[]'"`
	ContextWindow *int                 `gorm:"type:integer"`
	MaxOutput     *int                 `gorm:"type:integer"`
	Params        *ProviderModelParams `gorm:"serializer:json;type:jsonb"`
	Enabled       bool                 `gorm:"not null;default:true"`
	IsDefault     bool                 `gorm:"not null;default:false;index"`

	// Provider only exists to tie provider_id to providers.id with a foreign key
	// that drops the model together with the provider. It is never loaded or
	// written through this struct.
	Provider Provider `gorm:"foreignKey:ProviderID;references:ID;constraint:OnDelete:CASCADE"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName pins the table name to the plural used by the rest of the schema.
func (ProviderModel) TableName() string { return "provider_models" }

// providerModelDefaultIndexName is the partial unique index that keeps at most
// one default model per provider.
const providerModelDefaultIndexName = "idx_provider_models_provider_id_default"

// EnsureProviderModelDefaultIndex creates the partial unique index behind the
// "at most one default model per provider" rule. GORM tags cannot express a
// partial index, so it is created here; the table is new, so no existing row can
// collide with it.
func EnsureProviderModelDefaultIndex(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS " + providerModelDefaultIndexName +
			" ON provider_models (provider_id) WHERE is_default",
	).Error
}

// CreateProviderModel inserts a model. A display name left empty is filled from
// ModelID first, so every stored row carries a label a client can show.
func CreateProviderModel(ctx context.Context, db *gorm.DB, model *ProviderModel) error {
	if model.DisplayName == "" {
		model.DisplayName = model.ModelID
	}
	if model.Capabilities == nil {
		model.Capabilities = []string{}
	}
	return db.WithContext(ctx).Create(model).Error
}

// ListProviderModels returns the models of one provider, default model first so
// the one a caller picks when it expresses no preference leads.
func ListProviderModels(ctx context.Context, db *gorm.DB, providerID uuid.UUID) ([]ProviderModel, error) {
	models := []ProviderModel{}
	err := db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("is_default DESC").
		Order("model_id").
		Find(&models).Error
	if err != nil {
		return nil, err
	}
	return models, nil
}

// ProviderModelBackfillResult reports what one backfill run did.
type ProviderModelBackfillResult struct {
	// Created is how many model rows the run inserted.
	Created int
	// Existing is how many providers already had their model row, so nothing
	// was written for them.
	Existing int
}

// BackfillProviderModels gives every provider stored before the Provider/Model
// split one model row carrying the provider's flat columns: model_name becomes
// model_id, the sampling columns become params, the model is tagged llm and is
// the provider's default. The insert ignores a row that is already there, so the
// backfill is safe to re-run after a partial failure; the caller is responsible
// for the run flag.
func BackfillProviderModels(ctx context.Context, db *gorm.DB) (ProviderModelBackfillResult, error) {
	result := ProviderModelBackfillResult{}

	providers := []Provider{}
	if err := db.WithContext(ctx).Order("id").Find(&providers).Error; err != nil {
		return result, err
	}

	for i := range providers {
		provider := &providers[i]
		model := &ProviderModel{
			ID:           uuid.New(),
			ProviderID:   provider.ID,
			ModelID:      provider.ModelName,
			DisplayName:  provider.ModelName,
			Capabilities: []string{ProviderModelCapabilityLLM},
			Params: &ProviderModelParams{
				Temperature:      provider.Temperature,
				TopP:             provider.TopP,
				Seed:             provider.Seed,
				ReasoningEffort:  provider.ReasoningEffort,
				Thinking:         provider.Thinking,
				FrequencyPenalty: provider.FrequencyPenalty,
				PresencePenalty:  provider.PresencePenalty,
				Combine:          provider.Combine,
			},
			Enabled:   true,
			IsDefault: true,
		}
		inserted := db.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(model)
		if inserted.Error != nil {
			return result, inserted.Error
		}
		if inserted.RowsAffected == 0 {
			result.Existing++
			continue
		}
		result.Created++
	}
	return result, nil
}
