import json
import os

import boto3 as boto3
from nacl.exceptions import BadSignatureError
from nacl.signing import VerifyKey

PUBLIC_KEY = os.environ["DISCORD_PUBLIC_KEY"]
aws_lambda = boto3.client("lambda")

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


def get_command(payload) -> str:
    """Generates a command name from the payload.

    Args:
        payload (dict): The payload from Discord.

    Returns:
        str: The command name.
    """
    command_name = payload["data"]["name"]

    for option in payload["data"]["options"]:
        if option["type"] == 1:
            command_name += "." + option["name"]
            break

    return command_name


def handler(event, context):
    print("request: {}".format(json.dumps(event)))

    try:
        verify_signature(event)
    except Exception as e:
        return {
            "statusCode": 500,
            "headers": {"Content-Type": "application/json"},
            "body": json.dumps({"error": "Invalid signature"}),
        }

    # check if message is a ping
    body = json.loads(event.get("body"))
    if ping_pong(body):
        response = PING_PONG
    elif body["type"] == 2:
        aws_lambda.invoke(
            FunctionName=os.environ.get("PROOCESSING_LAMBDA_NAME"),
            InvocationType="Event",
            Payload=json.dumps(body),
        )

        response = {
            "type": RESPONSE_TYPES["ACK_WITH_SOURCE"],
        }

        command = get_command(body)

        if command in ["twilioconfig.token"]:
            response["data"] = {"flags": 64}

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps(response),
    }
