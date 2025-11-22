import json

from roomba.model.redis import get_ctx_redis, get_redis
from sanic import HTTPResponse, Request, Sanic, Websocket
from sanic_ext import render

app = Sanic(__name__)

# ruff: noqa


@app.middleware("request")
async def before_request(request: Request):
    request.ctx.redis = get_redis()


@app.middleware("response")
async def after_request(request: Request, response):
    await request.ctx.redis.close()


@app.route("/")
async def front(request: Request):
    return HTTPResponse("Hello, World!")


@app.route("/event", methods=["GET", "POST"])
async def event_handler(request: Request):
    print(request.method)
    payload = request.json
    print(payload)
    return HTTPResponse("", status=204)


async def handleBlock(redis, decoded):
    xyz_key = ":".join(
        [
            str(decoded.pop("x")),
            str(decoded.pop("y")),
            str(decoded.pop("z")),
        ]
    )
    print("Block", xyz_key, decoded)
    await redis.hset("roomba:map", xyz_key, json.dumps(decoded))


async def handleTurtle(redis, decoded):
    print("Turtle", decoded)
    await redis.hset("roomba:turtles", decoded["name"], json.dumps(decoded))
    # await redis.hset("roomba:turtle", decoded["id"], json.dumps(decoded))


@app.websocket("/eventpush")
async def eventpush(request: Request, ws: Websocket):
    redis = get_ctx_redis(request.ctx)

    async for msg in ws:
        try:
            decoded = json.loads(msg)
        except json.decoder.JSONDecodeError:
            print("Invalid JSON", msg)
            continue

        if "block" in decoded:
            await handleBlock(redis, decoded["block"])
        elif "turtle" in decoded:
            await handleTurtle(redis, decoded["turtle"])
        else:
            print("Unknown message", decoded)


@app.route("/areas", methods=["GET", "POST"])
async def areas(request: Request):
    if request.method == "POST":
        area = request.form.get("area")
        center = request.form.get("center")

        g.redis.sadd("areas", area)

    workcells = g.redis.smembers("areas")

    return render(
        "areas.html",
        workcells=workcells,
    )


@app.route("/areas/<area:str>")
async def area(request: Request, area: str):
    return render(
        "area.html",
        area=area,
    )
