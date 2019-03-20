# This Python file uses the following encoding: utf-8
import logging
import os

from flask import Flask
from raven.contrib.flask import Sentry

from gpbilling.model.handlers import (before_request, commit_sql, connect_redis, connect_sql, disconnect_redis,
                                      disconnect_sql)
from gpbilling.views import api

app = Flask(__name__)
app.secret_key = os.environ.get("APP_SECRET", os.environ.get("SECRET_KEY"))
app.config["SESSION_COOKIE_NAME"] = "gpbilling"

# Debug
app.config["PROPAGATE_EXCEPTIONS"] = True

if "DEBUG" in os.environ:
    app.config["DEBUG"] = True

if app.debug:
    app.config["TEMPLATES_AUTO_RELOAD"] = True

app.config["SENTRY_INCLUDE_PATHS"] = ["gpbilling"]
sentry = Sentry(
    app,
    dsn=app.config.get("SENTRY_DSN", None),
    logging=True,
    level=logging.ERROR,
)

# Handlers

app.before_request(connect_sql)
app.before_request(connect_redis)
app.before_request(before_request)
app.after_request(commit_sql)
app.teardown_request(disconnect_sql)
app.teardown_request(disconnect_redis)

# Routes

app.register_blueprint(api.blueprint)
