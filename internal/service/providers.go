package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
	"rqark-data-cloud/internal/utils"
)

// Column limits enforced before the database sees the values.
const (
	maxProviderNameLen   = 255
	maxModelNameLen      = 255
	maxProviderAPIKey    = 255
	maxProviderAPIBase   = 255
	maxProviderVendorLen = 32
	// A header name is bounded by the same 255 as the other text columns; a
	// value is allowed the 8 KiB net/http allows a request header line.
	maxProviderHeaderNameLen  = 255
	maxProviderHeaderValueLen = 8192
	reasoningEffortLow        = "low"
	reasoningEffortHigh       = "high"
	reasoningEffortMax        = "max"
	thinkingEnabled           = "enabled"
	thinkingDisabled          = "disabled"
)

// CreateProviderInput is the body accepted by POST /api/providers.
type CreateProviderInput struct {
	Name      string
	ModelName string
	APIKey    string
	APIBase   string
	// Vendor labels a known upstream; an empty value is stored as NULL.
	Vendor *string
	// Headers are extra request headers the upstream needs. An empty map is
	// stored as NULL.
	Headers          map[string]string
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
	Name      *string
	ModelName *string
	APIKey    *string
	APIBase   *string
	// Vendor replaces the label; a null clears it and an empty string is stored
	// as NULL.
	Vendor utils.Optional[string]
	// Headers replaces the whole header set; a null or an empty object clears
	// it.
	Headers          utils.Optional[map[string]string]
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
		Vendor:           normalizeVendor(in.Vendor),
		Headers:          normalizeHeaders(in.Headers),
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
	if in.Vendor.Set {
		provider.Vendor = normalizeVendor(in.Vendor.Value)
	}
	if in.Headers.Set {
		provider.Headers = normalizeHeaders(valueOf(in.Headers.Value))
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
	if in.Vendor != nil {
		if err := validateProviderVendor(*in.Vendor); err != nil {
			return err
		}
	}
	if err := validateProviderHeaders(in.Headers); err != nil {
		return err
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
	if in.Vendor.Value != nil {
		if err := validateProviderVendor(*in.Vendor.Value); err != nil {
			return err
		}
	}
	if in.Headers.Value != nil {
		if err := validateProviderHeaders(*in.Headers.Value); err != nil {
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

// validateProviderVendor checks that the vendor label fits its column. An empty
// label is allowed: it means no known upstream, which is stored as NULL.
func validateProviderVendor(vendor string) error {
	if len(vendor) > maxProviderVendorLen {
		return &ValidationError{Message: "vendor is too long."}
	}
	return nil
}

// ProviderHeaderVariables are the values a stored header may interpolate with
// the ${name} syntax. They let one header definition serve a whole
// conversation, so a client can send `x-opencode-session: ${session_id}` without
// the proxy knowing what that header means. The name is a variable of the call,
// not of the header: the same token works in any header an upstream asks for.
var ProviderHeaderVariables = []string{
	"session_id",
	"agent_id",
	"user_id",
	"provider_id",
	"model",
}

// providerHeaderPlaceholder matches one ${name} placeholder in a header value.
var providerHeaderPlaceholder = regexp.MustCompile(`\$\{([a-zA-Z0-9_]*)\}`)

// providerHeaderVariableSet is the lookup validation uses.
func providerHeaderVariableSet() map[string]bool {
	set := make(map[string]bool, len(ProviderHeaderVariables))
	for _, name := range ProviderHeaderVariables {
		set[name] = true
	}
	return set
}

// RenderProviderHeaderValue substitutes the ${name} placeholders in one stored
// header value with the values of the call. ok is false when a placeholder has
// no value for this call, so the caller can leave the header off entirely
// rather than send an empty or half-filled one.
func RenderProviderHeaderValue(value string, vars map[string]string) (string, bool) {
	missing := false
	rendered := providerHeaderPlaceholder.ReplaceAllStringFunc(value, func(match string) string {
		name := match[2 : len(match)-1]
		replacement, ok := vars[name]
		if !ok || replacement == "" {
			missing = true
			return ""
		}
		return replacement
	})
	if missing {
		return "", false
	}
	return rendered, true
}

// validateProviderHeaderPlaceholders checks that a value only names variables
// the proxy can fill. An unknown placeholder would be sent upstream verbatim, so
// it is refused at write time instead.
func validateProviderHeaderPlaceholders(headerName, value string) error {
	for _, match := range providerHeaderPlaceholder.FindAllStringSubmatch(value, -1) {
		name := match[1]
		if !providerHeaderVariableSet()[name] {
			return &ValidationError{Message: "headers carries an unknown placeholder ${" + name + "} in " + headerName + "."}
		}
	}
	if strings.Count(value, "${") != len(providerHeaderPlaceholder.FindAllString(value, -1)) {
		return &ValidationError{Message: "headers carries an unclosed placeholder in " + headerName + "."}
	}
	return nil
}

// validateProviderHeaders checks that every extra header can be put on an HTTP
// request upstream. The proxy writes them with net/http, which refuses a name
// that is not a token and a value carrying a line break, so the same rules are
// applied here and a bad pair is reported as a field error rather than turning
// into a failed call later.
func validateProviderHeaders(headers map[string]string) error {
	for name, value := range headers {
		if name == "" {
			return &ValidationError{Message: "headers must not carry an empty header name."}
		}
		if !isHeaderNameToken(name) {
			return &ValidationError{Message: "headers carries an invalid header name: " + name + "."}
		}
		if len(name) > maxProviderHeaderNameLen {
			return &ValidationError{Message: "headers carries a header name that is too long: " + name + "."}
		}
		if len(value) > maxProviderHeaderValueLen {
			return &ValidationError{Message: "headers carries a value that is too long: " + name + "."}
		}
		if !isHeaderValueSafe(value) {
			return &ValidationError{Message: "headers carries a value with a line break: " + name + "."}
		}
		if err := validateProviderHeaderPlaceholders(name, value); err != nil {
			return err
		}
	}
	return nil
}

// isHeaderNameToken reports whether name is an RFC 9110 token, the character
// set a header field name may use.
func isHeaderNameToken(name string) bool {
	for i := 0; i < len(name); i++ {
		if !isTokenByte(name[i]) {
			return false
		}
	}
	return true
}

// isTokenByte reports whether b is one of the token characters (RFC 9110,
// section 5.6.2).
func isTokenByte(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// isHeaderValueSafe reports whether value can be sent as a header value: no CR
// or LF, which would let a stored value start a second header.
func isHeaderValueSafe(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == '\r' || value[i] == '\n' {
			return false
		}
	}
	return true
}

// normalizeVendor turns an absent or empty label into nil, so the column stays
// NULL rather than holding an empty string that reads back as a vendor.
func normalizeVendor(vendor *string) *string {
	if vendor == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*vendor)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// normalizeHeaders turns a nil or empty header set into nil, so the column stays
// NULL rather than holding an empty JSON object.
func normalizeHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// valueOf dereferences an optional value, reading an explicit null as nil.
func valueOf[T any](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

// validateReasoningEffort checks the reasoning_effort enum.
func validateReasoningEffort(value string) error {
	if value != reasoningEffortLow && value != reasoningEffortHigh && value != reasoningEffortMax {
		return &ValidationError{Message: `reasoning_effort must be "low", "high" or "max".`}
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
