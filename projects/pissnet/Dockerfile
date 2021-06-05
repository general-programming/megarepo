FROM alpine:edge

# Basic setup.
RUN apk add --no-cache -X http://dl-cdn.alpinelinux.org/alpine/edge/testing unrealircd openssl && \
    mkdir -p /var/lib/unrealircd/data /var/lib/unrealircd/cache && \
    adduser -D -h /var/lib/unrealircd unrealircd && \
    chown -Rv unrealircd:unrealircd /var/lib/unrealircd /etc/unrealircd /usr/lib/unrealircd && \
    chmod -Rv a+rx /usr/bin

USER unrealircd

CMD ["unrealircd", "-F"]
