#!/usr/bin/env python3
while True:
    with open("ciscos.csv", "a") as f:
        model = input("Model: ")
        serial = input("Serial: ")
        mac = input("MAC: ")
        f.write(",".join([model, serial, mac]))
        f.write("\n")
