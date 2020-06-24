import requests
import os

twilio_auth = requests.auth.HTTPBasicAuth(
    os.environ["TWILIO_SID"],
    os.environ["TWILIO_AUTH_TOKEN"]
)

r = requests.get("https://preview.twilio.com/Numbers/AvailableNumbers", auth=twilio_auth)
print(r.json())
