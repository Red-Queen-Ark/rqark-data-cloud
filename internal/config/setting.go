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
	// AllowSelfRegistration controls whether POST /api/auth/register works. It is
	// off by default: the service is meant for an internal deployment where an
	// admin creates the accounts.
	AllowSelfRegistration bool
	// TaskAtGrace is how late an "at" task may still be dispatched. A task whose
	// instant passed longer ago than this is recorded as missed instead of run.
	TaskAtGrace time.Duration
	// InternalToken is the shared secret the agent cloud presents on the internal
	// claim and finalize endpoints. It is compared in constant time and is never
	// accepted in place of a user credential.
	InternalToken string
	// TaskRunTimeout is how long one run may take before it is abandoned.
	TaskRunTimeout time.Duration
	// TaskRunStaleAfter is the age at which a run that is still marked running
	// is swept to lost. It has to be longer than TaskRunTimeout, otherwise a run
	// that is merely slow would be swept while it is still working.
	TaskRunStaleAfter time.Duration
	// TaskRunTokenBuffer is added to TaskRunTimeout to size the run token, so the
	// token outlives the run it was issued for.
	TaskRunTokenBuffer time.Duration
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
	AllowSelfRegistration = GetEnvBool("ALLOW_SELF_REGISTRATION", false)
	TaskAtGrace = GetEnvDuration("TASK_AT_GRACE", 300*time.Second)

	InternalToken = GetEnv("INTERNAL_TOKEN", "")
	TaskRunTimeout = GetEnvDuration("TASK_RUN_TIMEOUT", 600*time.Second)
	TaskRunTokenBuffer = GetEnvDuration("TASK_RUN_TOKEN_BUFFER", 120*time.Second)
	// The stale sweep runs on the same clock as the run timeout, so a run is only
	// swept once it has had its whole allowance plus a grace period.
	TaskRunStaleAfter = GetEnvDuration("TASK_RUN_STALE_AFTER", TaskRunTimeout+TaskRunTokenBuffer)
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

// GetEnvBool reads a boolean from the environment. An unset, empty or
// unparseable value falls back, the same way GetEnvInt does.
func GetEnvBool(key string, fallback bool) bool {
	v, err := strconv.ParseBool(GetEnv(key, ""))
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
