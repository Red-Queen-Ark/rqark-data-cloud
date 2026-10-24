package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/config"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/repository"
	"gitea.carhyme.top/RedQueenArk/rqark-data-cloud/internal/utils"
)

// Redis key layout and lifetimes for the two kinds of opaque token. Only the
// SHA-256 hash of a token is ever used as a key, so a Redis dump does not hand
// out usable tokens.
const (
	refreshTokenPrefix = "auth:rt:"
	resetTokenPrefix   = "auth:reset:"
	resetUserPrefix    = "auth:reset:user:"
	resetTokenTTL      = time.Hour
)

// Login checks the credentials and issues an access and a refresh token. A
// login may be a username or an email address.
func Login(ctx context.Context, login, password string) (string, string, error) {
	var (
		user *repository.User
		err  error
	)
	if strings.Contains(login, "@") {
		user, err = repository.GetUserByEmail(ctx, config.DB, login)
	} else {
		user, err = repository.GetUserByUsername(ctx, config.DB, login)
	}
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return "", "", ErrInvalidCredentials
		}
		return "", "", err
	}

	if !user.IsActive || !utils.VerifyPassword(user.HashedPassword, password) {
		return "", "", ErrInvalidCredentials
	}

	return issueTokens(ctx, user.ID)
}

// Refresh consumes a refresh token and issues a replacement pair. The old
// token is gone whether or not the rest of the flow succeeds.
func Refresh(ctx context.Context, refreshToken string) (string, string, error) {
	if refreshToken == "" {
		return "", "", ErrInvalidToken
	}

	value, err := config.Redis.GetDel(ctx, refreshTokenPrefix+hashToken(refreshToken)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", "", ErrInvalidToken
		}
		return "", "", err
	}

	userID, err := uuid.Parse(value)
	if err != nil {
		return "", "", ErrInvalidToken
	}

	// The refresh endpoint sits outside the auth middleware, so it repeats the
	// is_active check the middleware would otherwise make.
	user, err := repository.GetUserByID(ctx, config.DB, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return "", "", ErrInvalidToken
		}
		return "", "", err
	}
	if !user.IsActive {
		return "", "", ErrInvalidToken
	}

	return issueTokens(ctx, user.ID)
}

// ForgotPassword writes a fresh reset token for the account that owns email.
// An unknown or disabled account is a no-op, so the caller can answer the same
// way in every case.
func ForgotPassword(ctx context.Context, email string) error {
	user, err := repository.GetUserByEmail(ctx, config.DB, email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil
		}
		return err
	}
	if !user.IsActive {
		return nil
	}

	token, err := newOpaqueToken()
	if err != nil {
		return err
	}
	hash := hashToken(token)

	// At most one reset token per user: drop the previous pair before writing
	// the new one.
	previous, err := config.Redis.GetDel(ctx, resetUserPrefix+user.ID.String()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	if previous != "" {
		if err := config.Redis.Del(ctx, resetTokenPrefix+previous).Err(); err != nil {
			return err
		}
	}

	if err := config.Redis.Set(ctx, resetTokenPrefix+hash, user.ID.String(), resetTokenTTL).Err(); err != nil {
		return err
	}
	if err := config.Redis.Set(ctx, resetUserPrefix+user.ID.String(), hash, resetTokenTTL).Err(); err != nil {
		return err
	}

	link := config.ResetPasswordURL + "?token=" + token
	message, err := passwordResetMessage(user.Email, user.Username, config.ProjectName, link, resetTokenTTL)
	if err != nil {
		return err
	}
	// A mail that cannot be delivered must not change the answer: the endpoint
	// replies 204 whether or not the address exists, and a failure here would
	// answer 500 for exactly the addresses that do exist.
	if err := mailer.Send(ctx, message); err != nil {
		log.Printf("service: send password reset mail to %s: %v", user.Email, err)
	}
	return nil
}

// ResetPassword consumes a reset token and stores the new password. The token
// is spent before the write, so a rejected password costs the user a new link.
func ResetPassword(ctx context.Context, token, newPassword string) error {
	if token == "" {
		return ErrInvalidToken
	}

	value, err := config.Redis.GetDel(ctx, resetTokenPrefix+hashToken(token)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrInvalidToken
		}
		return err
	}

	userID, err := uuid.Parse(value)
	if err != nil {
		return ErrInvalidToken
	}
	if err := config.Redis.Del(ctx, resetUserPrefix+userID.String()).Err(); err != nil {
		return err
	}

	user, err := repository.GetUserByID(ctx, config.DB, userID)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return ErrInvalidToken
		}
		return err
	}

	return setPassword(ctx, user, newPassword)
}

// issueTokens signs an access token for userID and stores a fresh refresh
// token under the hash of its own value.
func issueTokens(ctx context.Context, userID uuid.UUID) (string, string, error) {
	access, err := utils.SignAccessToken(userID, []byte(config.SecretKey), config.AccessTokenTTL)
	if err != nil {
		return "", "", err
	}

	refresh, err := newOpaqueToken()
	if err != nil {
		return "", "", err
	}
	key := refreshTokenPrefix + hashToken(refresh)
	if err := config.Redis.Set(ctx, key, userID.String(), config.RefreshTokenTTL).Err(); err != nil {
		return "", "", err
	}

	return access, refresh, nil
}

// newOpaqueToken returns 32 random bytes, hex encoded.
func newOpaqueToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// hashToken returns the hex SHA-256 of a token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
