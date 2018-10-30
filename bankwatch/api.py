import os
import logging
import time
import uuid
from redis import ConnectionPool, StrictRedis
from flask import Flask, jsonify, request, render_template, g

import stripe
from plaid.errors import PlaidError

from constants import CHARGE_TYPES
from common import plaid_client, get_transactions, load_accounts, push_plaid_transactions, push_discord_embed

# Logging configuration
if "DEBUG_FLOOD" in os.environ:
    logging.basicConfig(level=logging.DEBUG)
else:
    logging.basicConfig(level=logging.INFO)

# Setup
logging.getLogger("asyncio").setLevel(logging.DEBUG)
log = logging.getLogger(__name__)

# App
app = Flask(__name__)
redis_pool = ConnectionPool(
    host=os.environ.get('REDIS_PORT_6379_TCP_ADDR', os.environ.get('REDIS_HOST', '127.0.0.1')),
    port=int(os.environ.get('REDIS_PORT_6379_TCP_PORT', os.environ.get('REDIS_PORT', 6379))),
    db=int(os.environ.get('REDIS_DB', 0)),
    decode_responses=True
)

# Before/after request
@app.before_request
def connect_redis():
    g.redis = StrictRedis(connection_pool=redis_pool)

@app.before_request
def add_bank_key():
    g.add_bank_key = g.redis.get("bank:addkey")

    if g.add_bank_key:
        return

    g.add_bank_key = str(uuid.uuid4())
    g.redis.setex("bank:addkey", 60 * 60 * 24, g.add_bank_key)
    log.info("Bank key has been set to %s", g.add_bank_key)

@app.teardown_request
def disconnect_redis(result=None):
    if hasattr(g, "redis"):
        del g.redis

    return result

# Randomize this key on every run to ensure we are the only ones adding banks.

@app.route('/')
def rootpage():
    return "Plaid webhook handler API"

@app.route("/add_bank", methods=["GET"])
def add_bank():
    return render_template("add_bank.html",
        plaid_environment=os.environ["PLAID_ENV"],
        plaid_public_key=os.environ["PLAID_PUBLIC_KEY"],
        webhook_url=os.environ["WEBHOOK_URL"]
    )

def format_error(e):
    return {
        'error': {
            'display_message': e.display_message,
            'error_code': e.code,
            'error_type': e.type,
            'error_message': e.message
        }
    }

@app.route("/add_bank", methods=["POST"])
def add_bank_post():
    if not request.form:
        return jsonify({"error": "What are you doing here?"}), 400

    if request.form["addkey"] != g.add_bank_key:
        return jsonify({"error": "Bad add bank key?"}), 400

    public_token = request.form["public_token"]

    try:
        exchange_response = plaid_client.Item.public_token.exchange(public_token)
    except PlaidError as e:
        return jsonify(format_error(e)), 400

    item_id = exchange_response["item_id"]
    access_token = exchange_response["access_token"]

    g.redis.hset("bank:tokens", item_id, access_token)

    return jsonify({"result": "Success"})

@app.route("/" + os.environ.get("WEBHOOK_NAME", "inbound"), methods=["POST"])
def inbound_post():
    # Parse data.
    if "webhook_type" not in request.json or request.json["webhook_type"] != "TRANSACTIONS":
        return jsonify({"error": "invalid_type"}), 400

    item_id = request.json["item_id"]
    access_token = g.redis.hget("bank:tokens", item_id)
    if not access_token:
        return jsonify({"error": "token_not_in_db"}), 400

    # Get transactions.
    transactions = get_transactions(item_id, access_token, g.redis, get_inserted=False)

    # Drop request if there are no transactions.
    if not transactions:
        return jsonify("ok")

    # Push to Discord
    push_status, push_response = push_plaid_transactions(
        transactions,
        g.redis
    )

    return jsonify("ok")

@app.route('/stripe_test', methods=['POST'])
def stripe_test_post():
    return handle_stripe(True)

@app.route('/stripe', methods=['POST'])
def stripe_live_post():
    return handle_stripe()

def handle_stripe(is_test=False):
    # Boilerplate event parsing
    payload = request.data.decode('utf-8')
    received_sig = request.headers.get('Stripe-Signature', None)

    try:
        event = stripe.Webhook.construct_event(
            payload,
            received_sig,
            os.environ["STRIPE_TEST_WEBHOOK_SECRET"] if is_test else os.environ["STRIPE_WEBHOOK_SECRET"],
            api_key=os.environ["STRIPE_TEST_APIKEY"] if is_test else os.environ["STRIPE_APIKEY"]
        )
    except ValueError:
        log.info("Error while decoding event!")
        return 'Bad payload', 400
    except stripe.error.SignatureVerificationError:
        log.info("Invalid signature!")
        return 'Bad signature', 400

    data_object = event.data["object"]

    # Charge events.
    if event.type in CHARGE_TYPES:
        status = event.data["object"]["status"]

        if status in ("succeeded", "failed", "refunded"):
            amount = data_object["amount"] / 100
            amount_refunded = (data_object["amount_refunded"] or 0) / 100
            currency = data_object["currency"]
            failure_message = data_object["failure_message"]
            charge_description = data_object["description"]
            fields = []

            # Basic description text
            if not amount_refunded:
                desc_text = f"A {amount} {currency.upper()} charge has {status}."
            else:
                desc_text = f"A {amount} {currency.upper()} charge has been refunded."

            # Add transaction description field
            if charge_description:
                fields.append({
                    "name": "Description",
                    "value": charge_description,
                    "inline": True
                })

            # Add fields for failures/refunds
            if amount_refunded:
                fields.append({
                    "name": "Amount refunded",
                    "value": f"{amount_refunded} {currency.upper()}",
                    "inline": True
                })

            if failure_message:
                fields.append({
                    "name": "Failure message",
                    "value": failure_message,
                    "inline": True
                })

            push_discord_embed(
                title=f"{status.capitalize()} Stripe charge.",
                description=desc_text,
                fields=fields,
                testing=is_test
            )
    elif event.type == "charge.dispute.created":
        push_discord_embed(
            title=f"New Stripe dispute.",
            description=f"A dispute for {data_object['amount']} {data_object['currency'].upper()} has been opened.",
            testing=is_test
        )

    log.info("Received {environ} event: id={id}, type={type}".format(
        environ="test" if is_test else "live",
        id=event.id,
        type=event.type
    ))

    return "", 200

log.info("Webhook API started!")
