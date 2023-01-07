import os
import aioredis


def get_redis() -> aioredis.Redis:
    return aioredis.from_url(
        os.environ.get("REDIS_URL", "redis://localhost:6379/0"),
        decode_responses=True,
    )


def get_ctx_redis(ctx) -> aioredis.Redis:
    return ctx.redis
