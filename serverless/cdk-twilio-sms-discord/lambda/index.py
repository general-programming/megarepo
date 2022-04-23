import json
import os

from nacl.exceptions import BadSignatureError
from nacl.signing import VerifyKey
from parse import parse_slash_command

PUBLIC_KEY = os.environ["DISCORD_PUBLIC_KEY"]

PING_PONG = {"type": 1}
RESPONSE_TYPES = {
    "PONG": 1,
    "ACK_NO_SOURCE": 2,
    "MESSAGE_NO_SOURCE": 3,
    "MESSAGE_WITH_SOURCE": 4,
    "ACK_WITH_SOURCE": 5,
}


def verify_signature(event):
    raw_body = event.get("body")
    auth_sig = event["headers"].get("x-signature-ed25519")
    auth_ts = event["headers"].get("x-signature-timestamp")

    message = auth_ts.encode() + raw_body.encode()
    verify_key = VerifyKey(bytes.fromhex(PUBLIC_KEY))
    verify_key.verify(message, bytes.fromhex(auth_sig))  # raises an error if unequal


def ping_pong(body):
    if body.get("type") == 1:
        return True
    return False


def handler(event, context):
    print("request: {}".format(json.dumps(event)))

    try:
        verify_signature(event)
    except Exception as e:
        print(e)
        return {
            "statusCode": 500,
            "headers": {"Content-Type": "application/json"},
            "body": json.dumps({"error": "Invalid signature"}),
        }

    # check if message is a ping
    body = json.loads(event.get("body"))
    if ping_pong(body):
        return {
            "statusCode": 200,
            "headers": {"Content-Type": "application/json"},
            "body": json.dumps(PING_PONG),
        }

    # parse the payload
    if body["type"] == 2:
        message_response = parse_slash_command(body)

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps(
            {
                "type": RESPONSE_TYPES["MESSAGE_WITH_SOURCE"],
                "data": message_response,
            }
        ),
    }
