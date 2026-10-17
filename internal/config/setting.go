package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

var (
	ProjectName      string
	PostgresServer   string
	PostgresPort     int
	PostgresDB       string
	PostgresUser     string
	PostgresPassword string
	RedisServer      string
	RedisPort        int
	SecretKey        string
	ResetPasswordURL string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	// TaskAtGrace is how late an "at" task may still be dispatched. A task whose
	// instant passed longer ago than this is recorded as missed instead of run.
	TaskAtGrace time.Duration
)

func init() {
	_ = godotenv.Load()

	ProjectName = GetEnv("PROJECT_NAME", "")
	PostgresServer = GetEnv("POSTGRES_SERVER", "localhost")
	PostgresPort = GetEnvInt("POSTGRES_PORT", 5432)
	PostgresDB = GetEnv("POSTGRES_DB", "app")
	PostgresUser = GetEnv("POSTGRES_USER", "postgres")
	PostgresPassword = GetEnv("POSTGRES_PASSWORD", "")
	RedisServer = GetEnv("REDIS_SERVER", "localhost")
	RedisPort = GetEnvInt("REDIS_PORT", 6379)
	SecretKey = GetEnv("SECRET_KEY", "")
	ResetPasswordURL = GetEnv("RESET_PASSWORD_URL", "")
	AccessTokenTTL = 60 * time.Minute
	RefreshTokenTTL = 720 * time.Hour
	TaskAtGrace = GetEnvDuration("TASK_AT_GRACE", 300*time.Second)
}

func GetEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func GetEnvInt(key string, fallback int) int {
	v, err := strconv.Atoi(GetEnv(key, ""))
	if err != nil {
		return fallback
	}
	return v
}

// GetEnvDuration reads a whole number of seconds from the environment. An unset,
// empty, non-numeric or negative value falls back, the same way GetEnvInt does.
func GetEnvDuration(key string, fallback time.Duration) time.Duration {
	seconds, err := strconv.Atoi(GetEnv(key, ""))
	if err != nil || seconds < 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
