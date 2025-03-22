import os
import hvac
import logging
from twilio.rest import Client

log = logging.getLogger(__name__)

def get_twilio_credentials_from_vault():
    """Retrieve Twilio credentials from Vault."""
    try:
        client = hvac.Client()
        if not client.is_authenticated():
            log.warning("Vault client is not authenticated, falling back to environment variables")
            return None, None
            
        secret = client.secrets.kv.read_secret_version(path="secret/twilio-discord")
        data = secret["data"]["data"]
        return data.get("sid"), data.get("auth_token")
    except Exception as e:
        log.warning(f"Failed to retrieve Twilio credentials from Vault: {e}, falling back to environment variables")
        return None, None

def create_twilio() -> Client:
    # Try to get credentials from Vault first
    twilio_sid, twilio_auth_token = get_twilio_credentials_from_vault()
    
    # Fall back to environment variables if Vault retrieval fails
    if not twilio_sid:
        twilio_sid = os.environ["TWILIO_SID"]
    if not twilio_auth_token:
        twilio_auth_token = os.environ["TWILIO_AUTH_TOKEN"]

    return Client(twilio_sid, twilio_auth_token)
