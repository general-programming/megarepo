# This Python file uses the following encoding: utf-8
import logging
import os

from flask import Flask
from flask_cors import CORS
from flask_mail import Mail

import sentry_sdk
import stripe

from sentry_sdk.integrations.flask import FlaskIntegration
from werkzeug.contrib.fixers import ProxyFix

from gpbilling.model.handlers import (init_stripe, before_request, commit_sql, connect_redis, connect_sql, disconnect_redis,
                                      disconnect_sql)

# Setup sentry before app
sentry_sdk.init(
    dsn=os.environ.get("SENTRY_DSN", None),
    integrations=[FlaskIntegration()]
)


app = Flask(__name__)
app.secret_key = os.environ.get("APP_SECRET", os.environ.get("SECRET_KEY"))
app.config["SESSION_COOKIE_NAME"] = "gpbilling"
app.wsgi_app = ProxyFix(app.wsgi_app)
app.url_map.strict_slashes = False

# CORS
CORS(
    app=app,
    origins=[
        "https://pay.generalprogrmaming.org",
        "https://pay.catgirls.dev"
    ]
)

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
