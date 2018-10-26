import os
import logging
import time
import uuid
from redis import ConnectionPool, StrictRedis
from flask import Flask, jsonify, request, render_template, g

from plaid.errors import PlaidError

from common import plaid_client, get_transactions, load_accounts, push_transactions

# Logging configuration
if "DEBUG_FLOOD" in os.environ:
    logging.basicConfig(level=logging.DEBUG)
else:
    logging.basicConfig(level=logging.INFO)

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

@app.teardown_request
def disconnect_redis(result=None):
    if hasattr(g, "redis"):
        del g.redis

    return result

# Randomize this key on every run to ensure we are the only ones adding banks.
add_bank_key = str(uuid.uuid4())
print("Add bank key:", add_bank_key)

@app.route('/')
def rootpage():
    return "Plaid webhook handler API"

@app.route("/add_bank", methods=["GET"])
def add_bank():
    return render_template("add_bank.html",
        plaid_environment=os.environ["PLAID_ENV"],
        plaid_public_key=os.environ["PLAID_PUBLIC_KEY"],
        webhook_url="https://requestbin.nepeat.com/1lcl78j1"
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

    if request.form["addkey"] != add_bank_key:
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

    # Push to Discord
    push_status, push_response = push_transactions(
        transactions
    )

    if push_status != 204:
        log.error("Error from Discord: %s", push_response)

    return jsonify("ok")

log.info("Webhook API started!")
