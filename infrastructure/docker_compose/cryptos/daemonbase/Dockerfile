# Dockerfile credit goes to https://github.com/ruimarinho/docker-bitcoin-core/blob/master/0.15/Dockerfile

# Build stage for coin daemon
FROM ubuntu:16.04 as coindaemon

RUN apt-get update \
  && apt-get install -y software-properties-common \
  && add-apt-repository ppa:bitcoin/bitcoin \
  && apt-get update \
  && apt-get install -y wget libminiupnpc-dev libzmq3-dev libdb4.8-dev libdb4.8++-dev libboost-system-dev libboost-filesystem-dev libboost-chrono-dev libboost-program-options-dev libboost-test-dev libboost-thread-dev build-essential libtool autotools-dev automake pkg-config libssl-dev libevent-dev bsdmainutils python3 libgmp-dev

ARG COIN_TARBALL
ARG COIN_NAME
ENV COIN_PREFIX=/opt/coind-${COIN_NAME}

# Environment
COPY build.sh /
RUN ln -sv /usr/lib/libboost_thread-mt.a /usr/lib/libboost_thread.a \
  && ln -sv /usr/lib/libboost_thread-mt.so /usr/lib/libboost_thread.so

RUN cd /tmp \
  && mkdir code \
  && wget ${COIN_TARBALL} \
  && tar xfv *.tar.* -C code --strip-components 1 \
  && /build.sh

# Build stage for compiled artifacts
FROM ubuntu:16.04

# grab gosu for easy step-down from root
RUN set -x \
    && export GOSU_VERSION=1.10 \
    && apt-get update && apt-get install -y --no-install-recommends wget && rm -rf /var/lib/apt/lists/* \
    && wget --no-check-certificate -O /usr/local/bin/gosu "https://github.com/tianon/gosu/releases/download/$GOSU_VERSION/gosu-$(dpkg --print-architecture)" \
    && wget --no-check-certificate -O /usr/local/bin/gosu.asc "https://github.com/tianon/gosu/releases/download/$GOSU_VERSION/gosu-$(dpkg --print-architecture).asc" \
    && export GNUPGHOME="$(mktemp -d)" \
    && gpg --keyserver ha.pool.sks-keyservers.net --recv-keys B42F6819007F00F88E364FD4036A9C25BF357DD4 \
    && gpg --batch --verify /usr/local/bin/gosu.asc /usr/local/bin/gosu \
    && rm -r "$GNUPGHOME" /usr/local/bin/gosu.asc \
    && chmod +x /usr/local/bin/gosu \
    && gosu nobody true \
    && apt-get purge -y --auto-remove wget

RUN apt-get update \
  && apt-get install -y software-properties-common \
  && add-apt-repository ppa:bitcoin/bitcoin \
  && apt-get update \
  && apt-get install -y libdb4.8-dev libdb4.8++-dev libboost-system-dev libboost-filesystem-dev libboost-chrono-dev libboost-program-options-dev libboost-test-dev libboost-thread-dev libevent-dev libssl1.0.0 libzmq3-dev libminiupnpc10 libgmp-dev \
  && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*

ARG COIN_NAME
ENV COIN_DATA=/data
ENV COIN_PREFIX=/opt/coind-${COIN_NAME}
ENV PATH=${COIN_PREFIX}/bin:$PATH

# Environment
RUN ln -sv /usr/lib/libboost_thread-mt.so /usr/lib/libboost_thread.so

COPY --from=coindaemon /opt /opt
COPY entrypoint.sh /entrypoint.sh

VOLUME ["/data"]

EXPOSE 8332 8333

ENTRYPOINT ["/entrypoint.sh"]

CMD ["coind", "-dbcache=2048", "-printtoconsole", "-server", "-txindex", "-rpcport=8332", "-rpcuser=coinuser", "-rpcpassword=coinpassword", "-rpcallowip=0.0.0.0/0"]
