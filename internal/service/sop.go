package service

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"rqark-data-cloud/internal/config"
	"rqark-data-cloud/internal/repository"
)

const (
	// maxSopContentLen is the limit the content CHECK constraint enforces.
	maxSopContentLen = 2000
	// maxSopTitleLen is the width of the title column.
	maxSopTitleLen = 255
)

// sopSlugPattern is the whitelist a slug has to match: lowercase letters,
// digits and hyphens, never empty, never starting with a hyphen. It is what
// keeps a slug from naming anything but a file of the sop directory.
var sopSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// CreateSopInput is the body accepted by POST /api/sops.
type CreateSopInput struct {
	Slug    string
	Title   string
	Content string
}

// UpdateSopInput is the body accepted by PATCH /api/sops/{slug}. A nil field
// keeps what is stored.
type UpdateSopInput struct {
	Title   *string
	Content *string
}

// ListSops returns one page of the caller's sops, slug ascending.
func ListSops(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]repository.Sop, error) {
	if cursor != "" {
		if err := requireSopSlug(cursor); err != nil {
			return nil, err
		}
	}
	return repository.ListSops(ctx, config.DB, userID, cursor, limit)
}

// GetSop returns one of the caller's sops.
func GetSop(ctx context.Context, userID uuid.UUID, slug string) (*repository.Sop, error) {
	if err := requireSopSlug(slug); err != nil {
		return nil, err
	}
	return repository.GetSop(ctx, config.DB, userID, slug)
}

// CreateSop validates and stores one sop. A slug the caller already uses is
// reported as ErrSopSlugTaken rather than overwriting the stored procedure.
func CreateSop(ctx context.Context, userID uuid.UUID, in CreateSopInput) (*repository.Sop, error) {
	if err := requireSopSlug(in.Slug); err != nil {
		return nil, err
	}
	content, err := validateSopContent(in.Content)
	if err != nil {
		return nil, err
	}
	title, err := validateSopTitle(in.Title)
	if err != nil {
		return nil, err
	}
	if title == "" {
		title = sopTitle(content, in.Slug)
	}

	sop := &repository.Sop{
		ID:      uuid.New(),
		UserID:  userID,
		Slug:    in.Slug,
		Title:   title,
		Content: content,
	}
	if err := repository.CreateSop(ctx, config.DB, sop); err != nil {
		return nil, mapWriteError(err)
	}
	return sop, nil
}

// UpdateSop rewrites the title or content of one of the caller's sops. A nil
// field is left as stored, so a caller that only edits the markdown does not
// have to send the title back.
func UpdateSop(ctx context.Context, userID uuid.UUID, slug string, in UpdateSopInput) (*repository.Sop, error) {
	if err := requireSopSlug(slug); err != nil {
		return nil, err
	}
	current, err := repository.GetSop(ctx, config.DB, userID, slug)
	if err != nil {
		return nil, err
	}

	title := current.Title
	if in.Title != nil {
		title, err = validateSopTitle(*in.Title)
		if err != nil {
			return nil, err
		}
	}
	content := current.Content
	if in.Content != nil {
		content, err = validateSopContent(*in.Content)
		if err != nil {
			return nil, err
		}
	}
	return repository.UpdateSop(ctx, config.DB, userID, slug, title, content)
}

// DeleteSop removes one of the caller's sops.
func DeleteSop(ctx context.Context, userID uuid.UUID, slug string) error {
	if err := requireSopSlug(slug); err != nil {
		return err
	}
	return repository.DeleteSop(ctx, config.DB, userID, slug)
}

// requireSopSlug reports a slug that is not on the whitelist. A slug is used as
// a file name locally as well, so anything a path could climb with is refused
// here rather than stored.
func requireSopSlug(slug string) error {
	if !sopSlugPattern.MatchString(slug) {
		return &ValidationError{Message: "slug must be 1 to 64 characters of lowercase letters, digits and hyphens, and must not start with a hyphen."}
	}
	return nil
}

// validateSopContent checks the markdown against the column limit. Content is
// stored as sent; an over-long one is refused rather than truncated, because a
// procedure cut short would still be injected as if it were whole.
func validateSopContent(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", &ValidationError{Message: "content is required."}
	}
	if utf8.RuneCountInString(content) > maxSopContentLen {
		return "", &ValidationError{Message: "content must be at most 2000 characters."}
	}
	return content, nil
}

// validateSopTitle checks the title against the width of its column. An empty
// title is allowed and filled in from the content afterwards.
func validateSopTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > maxSopTitleLen {
		return "", &ValidationError{Message: "title must be at most 255 characters."}
	}
	return title, nil
}

// sopTitle derives a title from the content: the text of its first heading, or
// the slug when the markdown opens without one. It is the same rule the app
// applies to a local file, so a sop read from either source carries the same
// name in the slash panel.
func sopTitle(content, slug string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			title := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if title != "" {
				if utf8.RuneCountInString(title) > maxSopTitleLen {
					return truncateRunes(title, maxSopTitleLen)
				}
				return title
			}
		}
		break
	}
	return slug
}

// truncateRunes cuts s to n runes, which keeps a heading longer than the title
// column storable instead of refusing the whole procedure over its first line.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}
