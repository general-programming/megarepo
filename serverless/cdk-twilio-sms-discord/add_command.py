import os

import requests

url = "https://discord.com/api/v8/applications/966893011411824760/commands"

# This is an example CHAT_INPUT or Slash Command, with a type of 1
payloads = [
    {
        "name": "sms",
        "type": 1,
        "description": "Send a SMS to a phone number.",
        "options": [
            {
                "name": "phone_number",
                "description": "The phone number to send a SMS to.",
                "type": 3,
                "required": True,
            },
            {
                "name": "message",
                "description": "The message to send.",
                "type": 3,
                "required": False,
            },
            {
                "name": "attachment",
                "description": "Image or video to send with the SMS.",
                "type": 11,
                "required": False,
            },
        ],
    },
    {
        "name": "twilioconfig",
        "type": 1,
        "description": "[Administrator] Sets the Twilio configuration for the guild.",
        "options": [
            {
                "name": "token",
                "description": "Sets the guild's token configuration",
                "type": 1,  # 1 is type SUB_COMMAND
                "options": [
                    {
                        "name": "sid",
                        "description": "The Twilio SID.",
                        "type": 3,
                        "required": True,
                    },
                    {
                        "name": "token",
                        "description": "The Twilio token.",
                        "type": 3,
                        "required": True,
                    },
                ],
            },
            {
                "name": "number",
                "description": "Set the default phone number to send messages from.",
                "type": 1,  # 1 is type SUB_COMMAND
                "options": [
                    {
                        "name": "phone_number",
                        "description": "The phone number to send messages from.",
                        "type": 3,
                        "required": True,
                    },
                ],
            },
        ],
    },
    {
        "name": "messages",
        "type": 1,
        "description": "Shows historical text messages.",
        "options": [
            {
                "name": "before",
                "description": "Show messages below this ID.",
                "type": 4,
                "required": False,
            }
        ],
    },
]

# For authorization, you can use either your bot token
headers = {"Authorization": "Bot " + os.environ["DISCORD_TOKEN"]}

for payload in payloads:
    r = requests.post(url, headers=headers, json=payload)
    print("CREATE", r.text)

# Delete commands that do not exist in the payloads.
payload_commands = [x["name"] for x in payloads]
all_commands = requests.get(url, headers=headers).json()

for command in all_commands:
    if command["name"] not in payload_commands:
        r = requests.delete(f"{url}/{command['id']}", headers=headers)
        print("CHECK DELETE", command)
    else:
        print("CHECK EXISTS", command)
