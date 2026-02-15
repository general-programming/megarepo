package common

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

func NewRedis() *redis.Client {
	opt, err := redis.ParseURL(GetEnvWithDefault("REDIS_URL", "redis://localhost:6379"))
	if err != nil {
		panic(fmt.Sprintf("Failed to parse REDIS_URL: %s", err))
	}

	return redis.NewClient(opt)
}
