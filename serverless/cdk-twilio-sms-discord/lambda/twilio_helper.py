import logging

from twilio.rest import Client

log = logging.getLogger(__name__)


class Twilio:
    """
    Twilio helper class.
    """

    def __init__(self, from_number: str, sid: str, token: str):
        self.from_number = from_number
        self.client = Client(sid, token)

    def verify_token(self) -> bool:
        """Verify that the Twilio token is valid.

        Returns:
            bool: True if the token is valid, False otherwise.
        """
        try:
            self.client.incoming_phone_numbers.list()
        except Exception as e:
            log.error(e)
            return False

        return True

    def verify_number(self, phone_number: str) -> bool:
        """Verify that a phone number is registered in Twilio.

        Args:
            phone_number (str): Phone number in E.164 format.

        Throws:
            twilio.base.exceptions.TwilioException: Passed through Twilio Exception.

        Returns:
            bool: True if the number is in the account, False otherwise.
        """
        for number in self.client.incoming_phone_numbers.list(
            phone_number=phone_number
        ):
            if number.phone_number == phone_number:
                return True

        return False

    def send_sms(self, to: str, message: str, from_: str = None):
        """Send an SMS message.

        Args:
            to (str): Phone number to send the message to.
            message (str): Message to send.
            from_ (str, optional): Phone number to send the message from. Defaults to the phone number passed to the class.
        """
        self.client.messages.create(
            body=message,
            from_=from_ if from_ is not None else self.from_number,
            to=to,
        )

    def send_mms(self, to: str, media: str, message: str = None, from_: str = None):
        """Send an MMS message.

        Args:
            to (str): Phone number to send the message to.
            media (str): URL of the media to send.
            message (str, optional): Message to send. Defaults to None.
            from_ (str, optional): Phone number to send the message from. Defaults to the phone number passed to the class.
        """
        self.client.messages.create(
            body=message,
            from_=from_ if from_ is not None else self.from_number,
            to=to,
            media_url=media,
        )
