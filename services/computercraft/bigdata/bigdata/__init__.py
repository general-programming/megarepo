from flask import Flask, request

app = Flask(__name__)


@app.route("/")
def hello_world():
    return "Hello, World!"


@app.route("/event", methods=["GET", "POST"])
def event_handler():
    print(request.method)
    payload = request.get_json()
    print(payload)
    return "", 204
