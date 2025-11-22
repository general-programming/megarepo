import os
from uuid import uuid4

from flask import g, render_template
from flask_mail import Message as EmailMessage

from gpbilling import mail

expiry_times = {
    "verify": 86400,
    "reset": 600,
}

subjects = {
    "verify": "Verify your e-mail address",
}

keys = {
    "verify": "verify",
}


def send_email(action, email_address):
    email_token = str(uuid4())
    g.redis.setex(
        ":".join([keys[action], email_token]),
        expiry_times[action],
        ":".join([str(g.user.id), email_address]),
    )
    message = EmailMessage(
        subject=subjects[action],
        sender="support@generalprogramming.org",
        recipients=[email_address],
        body=render_template(
            "email/%s_plain.html" % action,
            base_domain=os.environ["BASE_DOMAIN"],
            email_token=email_token,
        ),
        html=render_template(
            "email/%s.html" % action,
            base_domain=os.environ["BASE_DOMAIN"],
            email_token=email_token,
        ),
    )
    mail.send(message)
