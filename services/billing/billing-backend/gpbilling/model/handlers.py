# This Python file uses the following encoding: utf-8
import os

import stripe
from flask import g, session
from redis import ConnectionPool, StrictRedis
from sqlalchemy.orm.exc import NoResultFound

from gpbilling.model import Account, sm

redis_pool = ConnectionPool(
    host=os.environ.get(
        "REDIS_PORT_6379_TCP_ADDR", os.environ.get("REDIS_HOST", "127.0.0.1")
    ),
    port=int(
        os.environ.get("REDIS_PORT_6379_TCP_PORT", os.environ.get("REDIS_PORT", 6379))
    ),
    db=int(os.environ.get("REDIS_DB", 0)),
    decode_responses=True,
)


def init_stripe():
    plans = {
        "email": {
            "amount": 1000,
            "interval": "month",
            "product": {"name": "Email service"},
            "nickname": "Email service",
            "currency": "usd",
        },
        "infra10": {
            "amount": 1000,
            "interval": "month",
            "product": {"name": "Infrastructure 10"},
            "nickname": "Infrastructure",
            "currency": "usd",
        },
    }

    existing_plans = stripe.Plan.list(limit=100)
    existing_ids = [x["id"] for x in existing_plans["data"]]

    for plan_id, plan_meta in plans.items():
        if plan_id not in existing_ids:
            stripe.Plan.create(id=plan_id, **plan_meta)


def connect_sql():
    g.db = sm()
    g.user = None

    if g.user_id is not None:
        try:
            g.user = g.db.query(Account).filter(Account.id == g.user_id).one()
        except NoResultFound:
            pass


def connect_redis():
    g.redis = StrictRedis(connection_pool=redis_pool)

    if "user_id" in session:
        g.user_id = session["user_id"]
    else:
        g.user_id = None


def before_request():
    pass


def commit_sql(response=None):
    # Don't commit on 4xx and 5xx.
    if response is not None and response.status[0] not in {"2", "3"}:
        g.db.rollback()
        return response
    if hasattr(g, "db"):
        g.db.commit()
    return response


def disconnect_sql(result=None):
    if hasattr(g, "db"):
        g.db.close()
        del g.db

    return result


def disconnect_redis(result=None):
    if hasattr(g, "redis"):
        del g.redis

    return result
