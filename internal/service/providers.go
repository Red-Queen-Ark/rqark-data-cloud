package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// Column limits enforced before the database sees the values.
const (
	maxProviderNameLen  = 255
	maxModelNameLen     = 255
	maxProviderAPIKey   = 255
	maxProviderAPIBase  = 255
	reasoningEffortHigh = "high"
	reasoningEffortMax  = "max"
	thinkingEnabled     = "enabled"
	thinkingDisabled    = "disabled"
)

// CreateProviderInput is the body accepted by POST /api/providers.
type CreateProviderInput struct {
	Name             string
	ModelName        string
	APIKey           string
	APIBase          string
	Priority         *int
	ReasoningEffort  *string
	Thinking         *string
	Temperature      *float64
	TopP             *float64
	Seed             *int
	FrequencyPenalty *float64
	PresencePenalty  *float64
	Combine          bool
	// IsPublic shares the provider with every other user. Only a superuser may
	// ask for it.
	IsPublic bool
}

// UpdateProviderInput is a partial update. A nil pointer for a NOT NULL column
// leaves the stored value alone; the nullable columns are utils.Optional, so an
// explicit null clears them while an absent key leaves them alone.
type UpdateProviderInput struct {
	Name             *string
	ModelName        *string
	APIKey           *string
	APIBase          *string
	Priority         *int
	ReasoningEffort  utils.Optional[string]
	Thinking         utils.Optional[string]
	Temperature      utils.Optional[float64]
	TopP             utils.Optional[float64]
	Seed             utils.Optional[int]
	FrequencyPenalty utils.Optional[float64]
	PresencePenalty  utils.Optional[float64]
	Combine          *bool
	// IsPublic clears or sets the share flag. Only a superuser may set it.
	IsPublic *bool
}

// CreateProvider validates in and stores a provider owned by userID. Sharing
// the provider (`IsPublic`) is reserved to superusers.
func CreateProvider(ctx context.Context, userID uuid.UUID, isSuperuser bool, in CreateProviderInput) (*repository.Provider, error) {
	if err := validateNewProvider(in); err != nil {
		return nil, err
	}
	if in.IsPublic && !isSuperuser {
		return nil, ErrProviderPublicForbidden
	}

	provider := &repository.Provider{
		ID:               uuid.New(),
		UserID:           userID,
		Name:             in.Name,
		ModelName:        in.ModelName,
		APIKey:           in.APIKey,
		APIBase:          in.APIBase,
		Priority:         *in.Priority,
		ReasoningEffort:  in.ReasoningEffort,
		Thinking:         in.Thinking,
		Temperature:      in.Temperature,
		TopP:             in.TopP,
		Seed:             in.Seed,
		FrequencyPenalty: in.FrequencyPenalty,
		PresencePenalty:  in.PresencePenalty,
		Combine:          in.Combine,
		IsPublic:         in.IsPublic,
	}
	if err := repository.CreateProvider(ctx, config.DB, provider); err != nil {
		return nil, err
	}
	return provider, nil
}

// UpdateProvider applies in to one of userID's providers. Setting the share flag
// is reserved to superusers; a non-superuser owner leaves it untouched or turns
// it off.
func UpdateProvider(ctx context.Context, userID uuid.UUID, isSuperuser bool, id uuid.UUID, in UpdateProviderInput) (*repository.Provider, error) {
	provider, err := repository.GetProviderByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}

	if err := validateProviderUpdate(in); err != nil {
		return nil, err
	}
	if in.IsPublic != nil && *in.IsPublic && !isSuperuser {
		return nil, ErrProviderPublicForbidden
	}

	if in.Name != nil {
		provider.Name = *in.Name
	}
	if in.ModelName != nil {
		provider.ModelName = *in.ModelName
	}
	if in.APIKey != nil {
		provider.APIKey = *in.APIKey
	}
	if in.APIBase != nil {
		provider.APIBase = *in.APIBase
	}
	if in.Priority != nil {
		provider.Priority = *in.Priority
	}
	if in.ReasoningEffort.Set {
		provider.ReasoningEffort = in.ReasoningEffort.Value
	}
	if in.Thinking.Set {
		provider.Thinking = in.Thinking.Value
	}
	if in.Temperature.Set {
		provider.Temperature = in.Temperature.Value
	}
	if in.TopP.Set {
		provider.TopP = in.TopP.Value
	}
	if in.Seed.Set {
		provider.Seed = in.Seed.Value
	}
	if in.FrequencyPenalty.Set {
		provider.FrequencyPenalty = in.FrequencyPenalty.Value
	}
	if in.PresencePenalty.Set {
		provider.PresencePenalty = in.PresencePenalty.Value
	}
	if in.Combine != nil {
		provider.Combine = *in.Combine
	}
	if in.IsPublic != nil {
		provider.IsPublic = *in.IsPublic
	}

	if err := repository.SaveProvider(ctx, config.DB, provider); err != nil {
		return nil, err
	}
	return provider, nil
}

// DeleteProvider removes one of userID's providers.
func DeleteProvider(ctx context.Context, userID, id uuid.UUID) error {
	return repository.DeleteProvider(ctx, config.DB, userID, id)
}

// ListProviders returns every provider the caller may use: the ones it owns
// plus the ones other users share, ordered by the priority that applies to the
// caller.
func ListProviders(ctx context.Context, userID uuid.UUID) ([]repository.ViewerProvider, error) {
	return repository.ListProviders(ctx, config.DB, userID)
}

// ListProviderSummaries returns the name and model_name of every provider
// userID may use: the ones it owns plus the ones other users share.
func ListProviderSummaries(ctx context.Context, userID uuid.UUID) ([]repository.ProviderSummary, error) {
	return repository.ListProviderSummaries(ctx, config.DB, userID)
}

// GetProvider loads a single provider the caller may use: one it owns, or one
// another user shares. The priority that applies to the caller is attached.
func GetProvider(ctx context.Context, userID, id uuid.UUID) (*repository.ViewerProvider, error) {
	provider, err := repository.GetVisibleProviderByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}

	view := repository.ViewerProvider{Provider: *provider}
	override, err := repository.GetProviderPriority(ctx, config.DB, userID, id)
	switch {
	case err == nil:
		view.OverridePriority = &override.Priority
	case errors.Is(err, repository.ErrProviderPriorityNotFound):
		// No override: the owner's priority applies.
	default:
		return nil, err
	}
	return &view, nil
}

// SetProviderPriority records the caller's own priority for a shared provider.
// The provider itself stays in its owner's hands: the override only changes the
// order in which this caller tries the providers it may use. A provider the
// caller owns carries no override, so asking for one is rejected: its priority
// is edited through the provider itself.
func SetProviderPriority(ctx context.Context, userID, id uuid.UUID, priority int) (*repository.ViewerProvider, error) {
	provider, err := repository.GetVisibleProviderByID(ctx, config.DB, userID, id)
	if err != nil {
		return nil, err
	}
	if provider.UserID == userID {
		return nil, &ValidationError{Message: "This provider is yours: set its priority with PUT /api/providers/" + id.String() + "."}
	}

	override, err := repository.UpsertProviderPriority(ctx, config.DB, userID, id, priority)
	if err != nil {
		return nil, err
	}
	return &repository.ViewerProvider{Provider: *provider, OverridePriority: &override.Priority}, nil
}

// DeleteProviderPriority drops the caller's override for a shared provider, so
// the owner's priority applies again.
func DeleteProviderPriority(ctx context.Context, userID, id uuid.UUID) error {
	provider, err := repository.GetVisibleProviderByID(ctx, config.DB, userID, id)
	if err != nil {
		return err
	}
	if provider.UserID == userID {
		return &ValidationError{Message: "This provider is yours: it carries no priority override to clear."}
	}
	return repository.DeleteProviderPriority(ctx, config.DB, userID, id)
}

// validateNewProvider checks every field of a create body. Priority and the
// three connection fields have no default, so they are required.
func validateNewProvider(in CreateProviderInput) error {
	if err := validateModelName(in.ModelName); err != nil {
		return err
	}
	if err := validateProviderAPIKey(in.APIKey); err != nil {
		return err
	}
	if err := validateProviderAPIBase(in.APIBase); err != nil {
		return err
	}
	if err := validateProviderName(in.Name); err != nil {
		return err
	}
	if in.Priority == nil {
		return &ValidationError{Message: "priority is required."}
	}
	if in.ReasoningEffort != nil {
		if err := validateReasoningEffort(*in.ReasoningEffort); err != nil {
			return err
		}
	}
	if in.Thinking != nil {
		if err := validateThinking(*in.Thinking); err != nil {
			return err
		}
	}
	return nil
}

// validateProviderUpdate checks only the fields a partial update carries.
func validateProviderUpdate(in UpdateProviderInput) error {
	if in.Name != nil {
		if err := validateProviderName(*in.Name); err != nil {
			return err
		}
	}
	if in.ModelName != nil {
		if err := validateModelName(*in.ModelName); err != nil {
			return err
		}
	}
	if in.APIKey != nil {
		if err := validateProviderAPIKey(*in.APIKey); err != nil {
			return err
		}
	}
	if in.APIBase != nil {
		if err := validateProviderAPIBase(*in.APIBase); err != nil {
			return err
		}
	}
	if in.ReasoningEffort.Value != nil {
		if err := validateReasoningEffort(*in.ReasoningEffort.Value); err != nil {
			return err
		}
	}
	if in.Thinking.Value != nil {
		if err := validateThinking(*in.Thinking.Value); err != nil {
			return err
		}
	}
	return nil
}

// validateProviderName checks that name fits its column. An empty name is
// allowed: the app replaces it with model_name when it reads the provider.
func validateProviderName(name string) error {
	if len(name) > maxProviderNameLen {
		return &ValidationError{Message: "name is too long."}
	}
	return nil
}

// validateModelName checks that modelName is present and fits its column.
func validateModelName(modelName string) error {
	if modelName == "" {
		return &ValidationError{Message: "model_name is required."}
	}
	if len(modelName) > maxModelNameLen {
		return &ValidationError{Message: "model_name is too long."}
	}
	return nil
}

// validateProviderAPIKey checks that apiKey is present and fits its column.
func validateProviderAPIKey(apiKey string) error {
	if apiKey == "" {
		return &ValidationError{Message: "api_key is required."}
	}
	if len(apiKey) > maxProviderAPIKey {
		return &ValidationError{Message: "api_key is too long."}
	}
	return nil
}

// validateProviderAPIBase checks that apiBase is present and fits its column.
func validateProviderAPIBase(apiBase string) error {
	if apiBase == "" {
		return &ValidationError{Message: "api_base is required."}
	}
	if len(apiBase) > maxProviderAPIBase {
		return &ValidationError{Message: "api_base is too long."}
	}
	return nil
}

// validateReasoningEffort checks the reasoning_effort enum.
func validateReasoningEffort(value string) error {
	if value != reasoningEffortHigh && value != reasoningEffortMax {
		return &ValidationError{Message: `reasoning_effort must be "high" or "max".`}
	}
	return nil
}

// validateThinking checks the thinking enum.
func validateThinking(value string) error {
	if value != thinkingEnabled && value != thinkingDisabled {
		return &ValidationError{Message: `thinking must be "enabled" or "disabled".`}
	}
	return nil
}
