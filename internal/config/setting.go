package config

import (
	"os"
	"strconv"

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
