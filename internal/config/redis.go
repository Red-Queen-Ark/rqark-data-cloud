package config

import (
	"context"
	"fmt"
	"log"

	"github.com/redis/go-redis/v9"
)

var Redis *redis.Client

func InitRedis() {
	client := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%d", RedisServer, RedisPort),
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("config: connect redis: %v", err)
	}

	Redis = client
}
