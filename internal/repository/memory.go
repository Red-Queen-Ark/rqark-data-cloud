package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrMemoryNotFound is returned when no memory row matches the lookup. It also
// covers a row owned by another user, so memory ids cannot be enumerated.
var ErrMemoryNotFound = errors.New("repository: memory not found")

// ErrPinnedLimit is returned when pinning a row would take the (user, agent)
// pair past MaxPinnedMemories.
var ErrPinnedLimit = errors.New("repository: pinned memory limit reached")

// MaxPinnedMemories is how many rows of one (user, agent) pair may be pinned.
const MaxPinnedMemories = 5

// MemorySearchLimit is how many rows a search returns. Search is not paginated:
// the caller injects one bounded window.
const MemorySearchLimit = 20

// MemoryPromptLimit is how many rows the injection prompt carries per turn.
const MemoryPromptLimit = 20

// The two kinds a memory row may carry. A fact is a long-lived statement about
// the user and is the only kind injected into the prompt. An event is the
// per-turn record of what the agent was asked and did: it is never injected,
// because it would fill the 20-row window with chatter, and search finds it
// when it is needed.
const (
	// MemoryKindFact marks a long-lived fact.
	MemoryKindFact = "fact"
	// MemoryKindEvent marks one turn's record.
	MemoryKindEvent = "event"
)

// EventRetention is how long an event row is kept. Events are a working record
// of what happened, not a permanent history, so old ones are deleted rather
// than kept at the cost of a growing table.
const EventRetention = 90 * 24 * time.Hour

// ValidMemoryKind reports whether kind is one of the two stored kinds.
func ValidMemoryKind(kind string) bool {
	return kind == MemoryKindFact || kind == MemoryKindEvent
}

// Memory is the memory table: one fact an agent recorded for one user. A row
// belongs to a (user, agent) pair and is never seen by another pair.
//
// The table is an append-only log as far as an agent is concerned: content,
// keywords, source and occurred_at are written once and never updated. Only
// pinned and the hit counter move afterwards, and only the owner deletes a row.
type Memory struct {
	ID      uuid.UUID `gorm:"type:uuid;primaryKey;index:idx_memory_user_agent_occurred,priority:4,sort:desc"`
	UserID  uuid.UUID `gorm:"type:uuid;not null;index:idx_memory_user_agent_occurred,priority:1"`
	AgentID uuid.UUID `gorm:"type:uuid;not null;index:idx_memory_user_agent_occurred,priority:2"`
	// Content is capped at 200 characters by a CHECK constraint, so a row that
	// is too long is rejected by the database as well as by the service.
	Content string `gorm:"type:text;not null;check:chk_memory_content_length,char_length(content) BETWEEN 1 AND 200"`
	// Keywords is a text[] so it can carry a GIN index and cannot hold anything
	// but strings.
	Keywords StringArray `gorm:"type:text[];not null;default:'{}';index:idx_memory_keywords,type:gin;check:chk_memory_keywords_count,array_length(keywords, 1) IS NULL OR array_length(keywords, 1) <= 8"`
	// Hits counts the days this row was injected. It is incremented at most once
	// per UTC day, and the score applied at query time decays it.
	Hits int `gorm:"type:integer;not null;default:0"`
	// Source is the platform the row was written from: TUI or AGENT_CLOUD.
	Source string `gorm:"type:varchar(32);not null;default:''"`
	// Kind separates a long-lived fact from one turn's event. Only fact rows
	// reach the injection prompt; events are read through search.
	Kind string `gorm:"type:text;not null;default:'fact';check:chk_memory_kind,kind IN ('fact','event')"`
	// SourceMessage is the original message the row was extracted from, kept so
	// a corrected transcript does not erase the evidence.
	SourceMessage json.RawMessage `gorm:"serializer:json;type:jsonb"`
	OccurredAt    time.Time       `gorm:"type:timestamptz;not null;index:idx_memory_user_agent_occurred,priority:3,sort:desc"`
	Pinned        bool            `gorm:"not null;default:false"`
	LastUsedAt    *time.Time      `gorm:"type:timestamptz"`
	CreatedAt     time.Time       `gorm:"not null"`
	UpdatedAt     time.Time       `gorm:"not null"`

	// User and Agent only exist to tie the two ids to their tables with
	// cascading foreign keys. They are never loaded or written through this
	// struct.
	User  User  `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:CASCADE"`
	Agent Agent `gorm:"foreignKey:AgentID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName pins the table name to the singular used by the design.
func (Memory) TableName() string { return "memory" }

// MemoryCursor is the position a keyset page continues from: the (occurred_at,
// id) pair of the last row of the previous page. The pair is unique because id
// breaks ties, so no row is served twice and none is skipped.
type MemoryCursor struct {
	OccurredAt time.Time
	ID         uuid.UUID
}

// StringArray is a PostgreSQL text[] column. Postgres is the only writer of the
// literal syntax, but it has to be parsed here because the stdlib driver hands
// an array back as a string.
type StringArray []string

// Value renders the array in PostgreSQL's text form. Every element is quoted,
// so commas, braces, quotes and backslashes inside a keyword survive.
func (a StringArray) Value() (driver.Value, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, element := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for j := 0; j < len(element); j++ {
			if element[j] == '\\' || element[j] == '"' {
				b.WriteByte('\\')
			}
			b.WriteByte(element[j])
		}
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}

// Scan reads the text form of a PostgreSQL array. The driver may hand it over
// as a string or as bytes, and a nil value means SQL NULL.
func (a *StringArray) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*a = nil
		return nil
	case string:
		parsed, err := parseStringArray(value)
		if err != nil {
			return err
		}
		*a = parsed
		return nil
	case []byte:
		parsed, err := parseStringArray(string(value))
		if err != nil {
			return err
		}
		*a = parsed
		return nil
	default:
		return fmt.Errorf("repository: cannot scan %T into a text array", src)
	}
}

// parseStringArray parses the array literal Postgres prints: braces around
// comma-separated elements, each optionally quoted, with a backslash escaping
// the next character inside a quoted element.
func parseStringArray(s string) (StringArray, error) {
	s = strings.TrimSpace(s)
	if s == "{}" {
		return StringArray{}, nil
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("repository: malformed text array %q", s)
	}

	inner := s[1 : len(s)-1]
	elements := make(StringArray, 0, strings.Count(inner, ",")+1)
	i := 0
	for {
		var element strings.Builder
		quoted := false
		if i < len(inner) && inner[i] == '"' {
			quoted = true
			i++
			closed := false
			for i < len(inner) {
				switch inner[i] {
				case '\\':
					if i+1 < len(inner) {
						element.WriteByte(inner[i+1])
						i += 2
						continue
					}
					element.WriteByte(inner[i])
					i++
				case '"':
					i++
					closed = true
				default:
					element.WriteByte(inner[i])
					i++
				}
				if closed {
					break
				}
			}
			if !closed {
				return nil, fmt.Errorf("repository: unterminated text array element in %q", s)
			}
		} else {
			for i < len(inner) && inner[i] != ',' {
				element.WriteByte(inner[i])
				i++
			}
		}

		value := element.String()
		if !quoted {
			value = strings.TrimSpace(value)
			if value == "NULL" {
				value = ""
			}
		}
		elements = append(elements, value)

		if i >= len(inner) {
			break
		}
		// Postgres never prints a trailing comma, so one is a malformed value.
		i++
		if i == len(inner) {
			return nil, fmt.Errorf("repository: trailing comma in text array %q", s)
		}
	}
	return elements, nil
}

// ListMemories returns one page of userID's memory, newest occurrence first.
// A non-nil agentID narrows the page to that agent; a nil one covers every agent
// of the user. An empty kind covers both kinds; a non-empty one narrows the page
// to it. A non-nil cursor resumes after the row it names.
func ListMemories(ctx context.Context, db *gorm.DB, userID uuid.UUID, agentID *uuid.UUID, kind string, cursor *MemoryCursor, limit int) ([]Memory, error) {
	query := db.WithContext(ctx).Where("user_id = ?", userID)
	if agentID != nil {
		query = query.Where("agent_id = ?", *agentID)
	}
	if kind != "" {
		query = query.Where("kind = ?", kind)
	}
	if cursor != nil {
		query = query.Where("(occurred_at, id) < (?, ?)", cursor.OccurredAt, cursor.ID)
	}

	memories := []Memory{}
	err := query.Order("occurred_at DESC").Order("id DESC").Limit(limit).Find(&memories).Error
	if err != nil {
		return nil, err
	}
	return memories, nil
}

// GetMemory loads one of userID's rows by primary key. Another user's row is
// reported as ErrMemoryNotFound.
func GetMemory(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) (*Memory, error) {
	var memory Memory
	if err := db.WithContext(ctx).First(&memory, "id = ? AND user_id = ?", id, userID).Error; err != nil {
		return nil, memoryLookupError(err)
	}
	return &memory, nil
}

// GetMemoriesInOrder loads the rows named by ids and returns them in that
// order. An id that no longer exists is skipped, so a row deleted between the
// ranking query and the re-read simply drops out of the page.
func GetMemoriesInOrder(ctx context.Context, db *gorm.DB, ids []uuid.UUID) ([]Memory, error) {
	memories := []Memory{}
	if len(ids) == 0 {
		return memories, nil
	}

	var rows []Memory
	if err := db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]Memory, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			memories = append(memories, row)
		}
	}
	return memories, nil
}

// CreateMemory inserts a row. A pinned row is counted under the same advisory
// lock the pin update takes, so two creates racing each other cannot both take
// the last free pin.
func CreateMemory(ctx context.Context, db *gorm.DB, memory *Memory) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if memory.Pinned {
			pinned, err := pinnedCount(ctx, tx, memory.UserID, memory.AgentID)
			if err != nil {
				return err
			}
			if pinned >= MaxPinnedMemories {
				return ErrPinnedLimit
			}
		}
		return tx.Create(memory).Error
	})
}

// SetMemoryPinned pins or unpins one of userID's rows. The count and the update
// run in one transaction under a per-(user, agent) advisory lock, because a
// count cannot be expressed as a CHECK constraint and a concurrent pin would
// otherwise slip past the limit.
func SetMemoryPinned(ctx context.Context, db *gorm.DB, userID, id uuid.UUID, pinned bool) (*Memory, error) {
	var memory Memory
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&memory, "id = ? AND user_id = ?", id, userID).Error; err != nil {
			return memoryLookupError(err)
		}
		if memory.Pinned == pinned {
			return nil
		}
		if pinned {
			count, err := pinnedCount(ctx, tx, memory.UserID, memory.AgentID)
			if err != nil {
				return err
			}
			if count >= MaxPinnedMemories {
				return ErrPinnedLimit
			}
		}
		if err := tx.Model(&Memory{}).Where("id = ?", memory.ID).Update("pinned", pinned).Error; err != nil {
			return err
		}
		memory.Pinned = pinned
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &memory, nil
}

// pinnedCount takes the advisory lock of one (user, agent) pair and returns how
// many of its rows are pinned. The lock is held until the surrounding
// transaction ends.
func pinnedCount(ctx context.Context, tx *gorm.DB, userID, agentID uuid.UUID) (int64, error) {
	key := userID.String() + ":" + agentID.String()
	if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(hashtext(?))", key).Error; err != nil {
		return 0, err
	}
	var count int64
	err := tx.WithContext(ctx).
		Model(&Memory{}).
		Where("user_id = ? AND agent_id = ? AND pinned", userID, agentID).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// DeleteMemory removes one of userID's rows. A row that does not exist is
// reported as ErrMemoryNotFound.
func DeleteMemory(ctx context.Context, db *gorm.DB, userID, id uuid.UUID) error {
	result := db.WithContext(ctx).Where("user_id = ?", userID).Delete(&Memory{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrMemoryNotFound
	}
	return nil
}

// memoryScore orders rows by relevance. The base of hits+1 keeps a row that was
// never injected in the running: without it a fresh row would score zero, never
// be injected and never earn a hit. GREATEST clamps a future occurred_at, which
// a client may send, because a negative age would inflate the score. pinned rows
// are sorted ahead of the expression by callers that want them.
const memoryScore = `(m.hits + 1) * power(0.5, GREATEST(0, EXTRACT(epoch FROM now() - m.occurred_at)) / 86400.0 / 30)`

// SearchMemories returns up to MemorySearchLimit rows of one (user, agent) pair.
// Rows are ranked by how many of keywords they carry, then by the decayed score,
// then by recency. query is an optional substring match over content or a
// keyword; at bounds occurred_at from above and is how a caller asks for what
// the user said before a given moment.
func SearchMemories(ctx context.Context, db *gorm.DB, userID, agentID uuid.UUID, query string, keywords []string, at *time.Time) ([]Memory, error) {
	// The keyword list travels as its text[] literal rather than as a slice: GORM
	// expands a slice argument into one placeholder per element, which would
	// turn ANY(?) into ANY($1,$2,...).
	keywordLiteral, err := StringArray(keywords).Value()
	if err != nil {
		return nil, err
	}

	sql := `SELECT m.*,
		COALESCE((SELECT count(*) FROM unnest(m.keywords) AS k WHERE k = ANY(?::text[])), 0) AS keyword_hits
		FROM memory m
		WHERE m.user_id = ? AND m.agent_id = ?`
	args := []any{keywordLiteral, userID, agentID}

	if query != "" {
		sql += ` AND (position(lower(?) in lower(m.content)) > 0
			OR EXISTS (SELECT 1 FROM unnest(m.keywords) AS k WHERE position(lower(?) in lower(k)) > 0))`
		args = append(args, query, query)
	}
	if at != nil {
		sql += ` AND m.occurred_at <= ?`
		args = append(args, *at)
	}

	sql += ` ORDER BY keyword_hits DESC, ` + memoryScore + ` DESC, m.occurred_at DESC, m.id DESC LIMIT ?`
	args = append(args, MemorySearchLimit)

	memories := []Memory{}
	if err := db.WithContext(ctx).Raw(sql, args...).Scan(&memories).Error; err != nil {
		return nil, err
	}
	return memories, nil
}

// PromptMemories returns the rows an agent injects on every turn: facts only,
// pinned first, then the highest decayed score, then the most recent. The window
// is fixed and never paginated, because the prompt is a bounded slice of the log.
func PromptMemories(ctx context.Context, db *gorm.DB, userID, agentID uuid.UUID) ([]Memory, error) {
	sql := `SELECT m.* FROM memory m
		WHERE m.user_id = ? AND m.agent_id = ? AND m.kind = 'fact'
		ORDER BY m.pinned DESC, ` + memoryScore + ` DESC, m.occurred_at DESC, m.id DESC
		LIMIT ?`

	memories := []Memory{}
	if err := db.WithContext(ctx).Raw(sql, userID, agentID, MemoryPromptLimit).Scan(&memories).Error; err != nil {
		return nil, err
	}
	return memories, nil
}

// DeleteExpiredEvents removes the event rows that occurred before cutoff and
// returns how many went. Facts are never touched, however old: a fact is what
// the log is for.
func DeleteExpiredEvents(ctx context.Context, db *gorm.DB, cutoff time.Time) (int64, error) {
	result := db.WithContext(ctx).Where("kind = ? AND occurred_at < ?", MemoryKindEvent, cutoff).Delete(&Memory{})
	return result.RowsAffected, result.Error
}

// TouchMemoryHits records one hit for each of ids, at most once per UTC day.
// last_used_at only moves when the row actually earned a hit, so repeated
// injections in one day cost one write and cannot inflate a score.
func TouchMemoryHits(ctx context.Context, db *gorm.DB, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	return db.WithContext(ctx).
		Model(&Memory{}).
		Where("id IN ?", ids).
		Where("last_used_at IS NULL OR (last_used_at AT TIME ZONE 'UTC')::date < (now() AT TIME ZONE 'UTC')::date").
		Updates(map[string]any{
			"hits":         gorm.Expr("hits + 1"),
			"last_used_at": gorm.Expr("now()"),
		}).Error
}

// memoryLookupError maps GORM's not-found error onto the repository's own.
func memoryLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrMemoryNotFound
	}
	return err
}
