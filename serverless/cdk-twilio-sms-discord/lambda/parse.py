import os
from typing import List

import boto3
from twilio.base.exceptions import TwilioException
from twilio_helper import Twilio
from util import convert_number_to_e164


def parse_command_options(options) -> dict:
    """Parse the command options.

    Args:
        options (dict): Command options from Discord.

    Returns:
        dict: The parsed command options.
    """
    output = {}

    for option in options:
        # type SUB_COMMAND is 1
        if option["type"] == 1:
            output[option["name"]] = parse_command_options(option["options"])
        else:
            output[option["name"]] = option["value"]

    return output


def hex_to_int(color: str) -> int:
    """Converts a hex color to an integer.

    Args:
        color (str): The hex color.

    Returns:
        int: The color as an integer.
    """
    return int(color, 16)


def get_ddb_table(tablename: str):
    """Gets the DynamoDB table.

    Args:
        tablename (str): The name of the table.

    Returns:
        DynamoDBServiceResource.Table: The DynamoDB table.
    """
    ddb = boto3.resource("dynamodb")
    return ddb.Table(os.environ["DDB_TABLE_" + tablename.upper()])


def create_embed(
    title: str,
    description: str,
    status: str,
    image: str = None,
    fields: List[dict] = None,
    footer_text: str = None,
):
    """Generates an embed.

    Args:
        title (str): The title of the embed.
        description (str): The description of the embed.
        status (str): User given status. Valid values are "success", and "failure".
        image (str, optional): The image URL. Defaults to None.
        fields (List[dict], optional): The fields of the embed.
        footer_text (str, optional): The footer text of the embed.

    Returns:
        dict: The embed.
    """
    if status == "success":
        color = hex_to_int("00FF00")
    elif status == "failure":
        color = hex_to_int("FF0000")

    output = {
        "title": title,
        "description": description,
        "color": color,
    }

    if footer_text:
        output["footer"] = {"text": footer_text}

    if fields:
        output["fields"] = fields

    if image:
        output["image"] = {"url": image}

    return output


def create_field(name: str, value: str, inline: bool = False) -> dict:
    """Generates a field for an embed.

    Args:
        name (str): The name of the field.
        value (str): The value of the field.
        inline (bool, optional): Whether the field should be inline. Defaults to False.

    Returns:
        dict: The embed field.
    """
    return {
        "name": name,
        "value": value,
        "inline": inline,
    }


def get_twilio(guild_id: int) -> Twilio:
    """Gets the Twilio helper for the guild.

    Args:
        guild_id (int): The Discord guild ID.

    Returns:
        Twilio: The Twilio helper object.
    """

    # Get the Twilio SID and token from the DDB table.
    table = get_ddb_table("config")
    response = table.get_item(Key={"guild_id": guild_id})
    config = response.get("Item", {})

    if not config or "twilio_sid" not in config or "twilio_token" not in config:
        raise ValueError("Twilio SID and token is not configured for this guild.")

    if "phone_number" not in config:
        raise ValueError("Default phone number is not configured for this guild.")

    return Twilio(
        config["phone_number"],
        config["twilio_sid"],
        config["twilio_token"],
    )


def verify_token(sid: str, token: str) -> bool:
    """Verifies the Twilio token.

    Args:
        sid (str): The Twilio SID.
        token (str): The Twilio token.

    Returns:
        bool: True if the token is valid, False otherwise.
    """
    client = Twilio(None, sid, token)
    return client.verify_token()


def handle_sms(options, payload):
    """Handles sending an SMS.

    Args:
        options (dict): Options for the command.
        payload (dict): The payload from Discord.

    Returns:
        dict: An embed.
    """
    twilio = get_twilio(int(payload["guild_id"]))

    # Attempt to get the attachment URL from the message
    attachment_id = options.get("attachment")
    attachment_url = None

    try:
        attachment_url = payload["data"]["resolved"]["attachments"][attachment_id][
            "url"
        ]
    except KeyError:
        pass

    # Get the phone number and message.
    phone_number = options.get("phone_number")
    message = options.get("message")

    # Return an error if the phone number is missing.
    if not phone_number:
        return create_embed("Error", "No phone number is provided.", "failure")

    # Return an error if the message or attachment is missing.
    if not message and not attachment_url:
        return create_embed("Error", "No message or attachment is provided.", "failure")

    try:
        fields = [
            create_field("From", twilio.from_number),
        ]

        if message:
            fields.append(create_field("Message", message))

        if attachment_url:
            twilio.send_mms(to=phone_number, message=message, media=attachment_url)
        else:
            twilio.send_sms(to=phone_number, message=message)

        return create_embed(
            "SMS",
            f"Message sent to {phone_number}",
            "success",
            image=attachment_url,
            fields=fields,
        )
    except Exception as e:
        print(e)
        return create_embed(
            "Error", f"Failed to send message to {phone_number}.", "failure"
        )


def handle_token_update(options, payload):
    """Updates the token in the database for a guild.

    Args:
        options (dict): Options for the command.
        payload (dict): The payload from Discord.

    Returns:
        dict: An embed.
    """
    token = options["token"]
    table = get_ddb_table("config")

    if verify_token(token["sid"], token["token"]):
        table.update_item(
            Key={"guild_id": int(payload["guild_id"])},
            UpdateExpression="set twilio_sid = :r1, twilio_token = :r2",
            ExpressionAttributeValues={
                ":r1": token["sid"],
                ":r2": token["token"],
            },
            ReturnValues="UPDATED_NEW",
        )
        return create_embed(
            "Success",
            f"Twilio token updated.",
            status="success",
        )
    else:
        return create_embed(
            "Error",
            f"Twilio token is invalid",
            "failure",
        )


def handle_number_update(options, payload):
    """Updates the phone number in the database for a guild.

    Args:
        options (dict): Options for the command.
        payload (dict): The payload from Discord.

    Returns:
        dict: An embed.
    """

    twilio = get_twilio(int(payload["guild_id"]))
    table = get_ddb_table("config")
    phone_number = options["number"]["phone_number"]
    e164_phone_number = convert_number_to_e164(phone_number)

    try:
        if twilio.verify_number(e164_phone_number):
            table.update_item(
                Key={"guild_id": int(payload["guild_id"])},
                UpdateExpression="set phone_number = :phone_number",
                ExpressionAttributeValues={
                    ":phone_number": e164_phone_number,
                },
                ReturnValues="UPDATED_NEW",
            )
            return create_embed(
                "Success",
                f"Phone number updated to '{e164_phone_number}'",
                "success",
            )
    except TwilioException as e:
        return create_embed(
            "Error",
            f"Failed to verify number: {e}",
            "failure",
        )

    return create_embed(
        "Error",
        f"Phone number '{e164_phone_number}' cannot be found in the Twilio account.",
        "failure",
    )


COMMANDS = {
    "sms": handle_sms,
    "twilioconfig.token": handle_token_update,
    "twilioconfig.number": handle_number_update,
}


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


def parse_slash_command(payload):
    """Parses a slash command payload.

    Args:
        payload (dict): The payload from Discord.

    Returns:
        dict: A `data` payload to return to Discord. https://discord.com/developers/docs/interactions/receiving-and-responding#interaction-response-object-interaction-response-structure
    """
    command = get_command(payload)
    options = parse_command_options(payload["data"]["options"])

    handler = COMMANDS.get(command)
    if handler:
        try:
            embed = handler(options, payload)
        except ValueError as e:
            embed = create_embed("Error", str(e), "failure")
    else:
        embed = create_embed("Error", f"Command {command} is not supported.", "failure")

    result = {
        "tts": False,
        "embeds": [embed],
        "allowed_mentions": [],
    }

    if command in ["twilioconfig.token"]:
        result["flags"] = 64

    return result
