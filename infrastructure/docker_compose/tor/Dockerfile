FROM alpine:edge

RUN apk add --no-cache libevent libressl2.6-libcrypto libressl2.6-libssl zlib zstd zstd-dev xz-libs zlib-dev

RUN apk --update add --virtual build-dependencies git build-base automake autoconf libevent-dev libressl-dev \
  && cd /tmp \
  && git clone https://github.com/torproject/tor.git \
  && cd tor \
  && ./autogen.sh \
  && ./configure --disable-asciidoc \
  && make -j4 \
  && make install \
  && cd .. \
  && rm -rf tor \
  && apk del build-dependencies \
  && rm -rf /var/cache/apk/*

CMD tor -f /etc/tor/torrc
