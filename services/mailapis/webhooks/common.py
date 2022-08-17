import hashlib
import os
import re

import aiohttp


async def push_mail(
    mail_image,
    mail_raw,
    subject="[Blank subject]",
    sent_from="[Blank sender]",
    sent_to=None,
):
    # Sent to mail
    if not sent_to:
        sent = "Sent to an unknown email"
        sent_to = "Unknown"
    else:
        sent = f"Sent to {sent_to}"

    # Gravatar URL
    emails = re.findall(r"([a-zA-Z0-9_.+-]+@[a-zA-Z0-9-]+\.[a-zA-Z0-9-.]+)", sent_from)

    if not emails:
        from_hash = "00000000000000000000000000000000"
    else:
        from_hash = hashlib.md5(emails[-1].encode("utf8").lower()).hexdigest()

    avatar_url = f"https://www.gravatar.com/avatar/{from_hash}?default=identicon&s=256"

    async with aiohttp.ClientSession() as session:
        r = await session.post(
            os.environ["DISCORD_WEBHOOK"],
            json={
                "username": sent_from,
                "avatar_url": avatar_url,
                "embeds": [
                    {
                        # Ass bleach pink
                        "color": 0xFFB9EC,
                        "title": subject,
                        "url": mail_raw,
                        "author": {"name": sent_from, "url": mail_raw},
                        "image": {"url": mail_image, "width": 1024},
                        "footer": {"text": sent},
                    }
                ],
            },
        )

    return r.status, await r.text()
