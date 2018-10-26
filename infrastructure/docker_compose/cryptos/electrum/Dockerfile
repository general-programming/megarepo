FROM alpine:edge

RUN apk add --no-cache python3 libevent libressl libressl2.6-libssl zlib zstd xz-libs
RUN apk add --no-cache --repository http://nl.alpinelinux.org/alpine/edge/testing leveldb

RUN apk --no-cache add --virtual build-dependencies git build-base automake autoconf libevent-dev libressl-dev zlib-dev zstd-dev python3-dev \
  && apk add --no-cache --repository http://nl.alpinelinux.org/alpine/edge/testing leveldb-dev \
  && cd / \
  && git clone https://github.com/kyuupichan/electrumx.git \
  && cd electrumx \
  && python3 setup.py develop \
  && apk del build-dependencies leveldb-dev

ADD coins.py /custom_coins.py
RUN cat /custom_coins.py >> /electrumx/lib/coins.py

ADD start.sh /start.sh
ADD electrum_banner /electrum_banner

VOLUME ["/data"]
WORKDIR /data

CMD /start.sh
