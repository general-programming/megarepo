import asyncio
from roomba.model.redis import get_redis
import json
from collections import defaultdict
import termcolor


def reprPoint(
    point: dict,
    char: str = "█",
):
    if point.get("obstructed", False):
        return termcolor.colored(char, "red", "on_red")

    if point.get("modem", False):
        return termcolor.colored(char, "blue", "on_blue")

    if point.get("farmFiducial", False):
        return termcolor.colored(char, "green", "on_green")

    if point.get("turtlePath", False):
        return termcolor.colored(char, "white", "on_white")

    return " "


async def main():
    redis = get_redis()
    point1 = (337, -122)
    point2 = (374, -95)

    while True:
        await asyncio.sleep(1)
        floormap = await redis.hgetall("roomba:map")
        turtles_by_pos = {}
        for turtle_id, turtle in (await redis.hgetall("roomba:turtles")).items():
            turtle = json.loads(turtle)
            tkey = f"{turtle['x']},{turtle['z']}"
            turtles_by_pos[tkey] = turtle

        decoded_map = defaultdict(lambda: {})

        for key, value in floormap.items():
            x, y, z = key.split(":", 3)
            x = int(x)
            y = int(y)
            z = int(z)

            # ensure the points are within the bounds
            if not (x >= point1[0] and x <= point2[0]):
                continue

            if not (z >= point1[1] and z <= point2[1]):
                continue

            # decode the value
            value = json.loads(value)

            # store the map point in the dict
            decoded_map[x][z] = value

        # print the map in a nice format
        for z in range(point1[1], point2[1] + 1):
            for x in range(point1[0], point2[0] + 1):
                try:
                    char = "█"
                    if f"{x},{z}" in turtles_by_pos:
                        char = "T"
                    pp = reprPoint(decoded_map[x][z], char)
                    # print(pp, decoded_map[x][z])
                    print(pp, end="")
                except KeyError:
                    print("?", end="")
            print()
        print("-" * 80)


if __name__ == "__main__":
    asyncio.run(main())
