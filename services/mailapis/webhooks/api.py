import email
import email.policy
import logging
import os

from aiohttp import ClientSession, web

from common import push_mail

# Logging configuration
if "DEBUG_FLOOD" in os.environ:
    logging.basicConfig(level=logging.DEBUG)
else:
    logging.basicConfig(level=logging.INFO)

logging.getLogger("asyncio").setLevel(logging.DEBUG)
log = logging.getLogger(__name__)

routes = web.RouteTableDef()

RENDER_ENDPOINT = os.environ["RENDER_ENDPOINT"] + "/render"


@routes.get("/")
async def rootpage(request):
    return web.Response(text="mail webhook api")


@routes.post("/inbound")
async def inbound_post(request):
    # Parse data.
    data = await request.post()
    log.debug(data)

    # Parse mail.
    message = email.message_from_string(data["email"], policy=email.policy.default)
    mail_body = message.get_body().get_payload(decode=True)

    if not mail_body:
        for part in message.walk():
            if part.get_content_type() == "text/html":
                mail_body = part.get_payload(decode=True)
                break

    if not mail_body:
        log.error("Unable to find mail body.")
        return web.json_response("ok")

    try:
        mail_body = mail_body.decode("utf8")
    except UnicodeDecodeError:
        pass

    # Render mail
    async with ClientSession() as session:
        async with session.post(
            RENDER_ENDPOINT,
            json={"auth": os.environ["AUTH_KEY"], "html": mail_body},
        ) as response:
            try:
                reply = await response.json()
            except:
                reply = await response.text()
                log.error("Error parsing response from render API: %s", reply)
                return web.json_response({"error": "bad_parse"})

            if "error" in reply:
                log.error("Got error from render API: %s", reply["error"])
                return web.json_response({"error": reply["error"]})

            mail_image = reply["image_url"]
            mail_raw = reply["raw_url"]

    # Push to Discord
    print(data)
    push_status, push_response = await push_mail(
        mail_image=mail_image,
        mail_raw=mail_raw,
        subject=data.get("subject", None),
        sent_from=data.get("from", None),
        sent_to=data.get("to", message.get_unixfrom()),
    )

    if push_status != 204:
        log.error("Error from Discord: %s", push_response)

    return web.json_response("ok")


app = web.Application(
    client_max_size=1024
    * 1024
    * 16  # Default of 2MB crippled some emails. 16MB should be "enough"
)
app.add_routes(routes)

log.info("Webhook API started!")
web.run_app(app)
