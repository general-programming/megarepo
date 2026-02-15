package storage

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/general-programming/gocommon"
	"github.com/redis/go-redis/v9"
)

type RedisClient struct {
	Client *redis.Client
}

var WrappedRedis *RedisClient

func InitRedis() {
	opt, err := redis.ParseURL(gocommon.GetEnvWithDefault("REDIS_URL", "redis://localhost:6379"))
	if err != nil {
		panic(fmt.Sprintf("Failed to parse REDIS_URL: %s", err))
	}

	WrappedRedis = &RedisClient{
		Client: redis.NewClient(opt),
	}
}

func (r *RedisClient) AppendLog(ctx context.Context, key string, values map[string]string) error {
	// the minimum age of events is 1 hour ago.
	// we set the minimum ID to that in order to allow for trimming based on that time stamp.
	// the volume of data is high enough that only an hour of data can be kept in ram buffer
	minAge := strconv.FormatInt(time.Now().Add(time.Duration(-1)*time.Hour).UnixMilli(), 10)

	_, err := r.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: key,
		Values: values,
		MinID:  minAge,
	}).Result()

	return err
}
