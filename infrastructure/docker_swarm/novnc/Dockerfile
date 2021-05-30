FROM ubuntu:20.04

WORKDIR /app

RUN apt-get update \
    && apt-get install -y git-core python3 \
    && git clone https://github.com/novnc/noVNC.git /app

