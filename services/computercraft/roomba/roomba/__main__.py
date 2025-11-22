import os

from roomba import app

if __name__ == "__main__":
    if "DEV" in os.environ:
        app.run(dev=True, port=5001)
    else:
        app.run(port=5001)
