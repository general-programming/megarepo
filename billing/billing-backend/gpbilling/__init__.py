# This Python file uses the following encoding: utf-8
import logging
import os

from flask import Flask
from flask_mail import Mail

import stripe

from werkzeug.contrib.fixers import ProxyFix
from raven.contrib.flask import Sentry

from gpbilling.model.handlers import (init_stripe, before_request, commit_sql, connect_redis, connect_sql, disconnect_redis,
                                      disconnect_sql)

app = Flask(__name__)
app.secret_key = os.environ.get("APP_SECRET", os.environ.get("SECRET_KEY"))
app.config["SESSION_COOKIE_NAME"] = "gpbilling"
app.wsgi_app = ProxyFix(app.wsgi_app)

# Mail
app.config["MAIL_SERVER"] = os.environ.get("MAIL_SERVER", "localhost")
app.config["MAIL_PORT"] = int(os.environ.get("MAIL_PORT", 25))
app.config["MAIL_USE_TLS"] = "MAIL_USE_TLS" in os.environ
app.config["MAIL_USERNAME"] = os.environ.get("MAIL_USERNAME")
app.config["MAIL_PASSWORD"] = os.environ.get("MAIL_PASSWORD")
app.config["MAIL_SUPPRESS_SEND"] = app.testing or bool(os.environ.get("NOMAIL"))
mail = Mail(app)

# Stripe
stripe.api_key = os.environ["STRIPE_KEY"]

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

app.before_first_request(init_stripe)
app.before_request(connect_redis)
app.before_request(connect_sql)
app.before_request(before_request)
app.after_request(commit_sql)
app.teardown_request(disconnect_sql)
app.teardown_request(disconnect_redis)

# Routes
from gpbilling.views import api

app.register_blueprint(api.blueprint)
