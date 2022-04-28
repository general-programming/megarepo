import json
import os

import requests
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


def handler(event, context):
    print("request: {}".format(json.dumps(event)))

    interaction_token = event["token"]
    application_id = event["application_id"]

    message_response = parse_slash_command(event)

    url = f"https://discord.com/api/v8/webhooks/{application_id}/{interaction_token}"
    requests.post(url, json=message_response)
