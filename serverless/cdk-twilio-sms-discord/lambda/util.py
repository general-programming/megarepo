def convert_number_to_e164(phone_number: str) -> str:
    """Convert a phone number into E.164 format.

    Args:
        phone_number (str): Phone number to convert.

    Returns:
        str: Phone number in E.164 format.
    """
    return "+" + phone_number.replace("-", "").replace(" ", "").replace("+", "")
