import os
from twilio.rest import Client

def create_twilio() -> Client:
    twilio_sid = os.environ["TWILIO_SID"]
    twilio_auth_token = os.environ["TWILIO_AUTH_TOKEN"]

    return Client(twilio_sid, twilio_auth_token)
