FROM python:3-alpine

# Edge repos
RUN echo http://dl-cdn.alpinelinux.org/alpine/edge/main > /etc/apk/repositories && \
    echo http://dl-cdn.alpinelinux.org/alpine/edge/community >> /etc/apk/repositories && \
    echo http://dl-cdn.alpinelinux.org/alpine/edge/testing >> /etc/apk/repositories

# Requirements
ENV PUPPETEER_SKIP_CHROMIUM_DOWNLOAD true

COPY requirements.txt /app/requirements.txt
RUN apk add --no-cache build-base libffi libffi-dev ca-certificates pkgconf tini chromium nss && \
	pip3 install -r /app/requirements.txt && \
	apk --no-cache del build-base libffi-dev

# Workdir
WORKDIR /app

# Add code
COPY . /app

# Tini entrypoint
ENTRYPOINT ["/sbin/tini", "--"]

