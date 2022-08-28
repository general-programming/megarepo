import sys

import twilio

from common import create_twilio

PHONE_NUMBERPAD = [
    ("abc", 2),
    ("def", 3),
    ("ghi", 4),
    ("jkl", 5),
    ("mno", 6),
    ("pqrs", 7),
    ("tuv", 8),
    ("wxyz", 9),
]
PHONE_NUMBERPAD_MAP = {c: v for k, v in PHONE_NUMBERPAD for c in k}

twilio_client = create_twilio()


def get_countries():
    result = []

    for record in twilio_client.available_phone_numbers.list():
        if "local" in record.subresource_uris:
            result.append(record.country_code)

    return result


def map_to_keypad(input: str) -> str:
    input = input.lower()
    result = ""

    if input.isdecimal():
        return input

    for char in input:
        if char.isdecimal():
            result += char
        elif char in PHONE_NUMBERPAD_MAP:
            result += str(PHONE_NUMBERPAD_MAP[char])
        else:
            print("WTF IS " + char)

    return result


# countries = ["US"]
countries = get_countries()
thing_to_search = map_to_keypad(sys.argv[1])
print(thing_to_search)

for country in countries:
    print(country)

    try:
        numbers = twilio_client.available_phone_numbers(country).local.list(
            beta=True, contains=thing_to_search
        )
    except twilio.base.exceptions.TwilioException as e:
        print(e)
        continue

    for number in numbers:
        print(number.friendly_name)


# twilio.

# print(get_countries())
