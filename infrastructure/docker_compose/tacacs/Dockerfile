FROM alpine:latest as base
MAINTAINER David Chidell (dchidell@cisco.com)
ENV VERSION=202205051820
ENV TAC_PLUS_BIN=/tacacs/sbin/tac_plus
ENV CONF_FILE=/etc/tac_plus/tac_plus.cfg


FROM base as build
RUN apk add --no-cache \
    build-base bzip2 perl perl-digest-md5 perl-ldap
ADD http://www.pro-bono-publico.de/projects/src/DEVEL.$VERSION.tar.bz2 /tac_plus.tar.bz2
RUN tar -xjf /tac_plus.tar.bz2 && \
    cd /PROJECTS && \
    ./configure --prefix=/tacacs && \
    make && \
    make install


FROM base

COPY --from=build /tacacs /tacacs
COPY entrypoint.sh /entrypoint.sh

RUN apk add --no-cache perl perl-digest-md5 perl-ldap && \
    chmod u+x /entrypoint.sh

COPY config /etc/tac_plus/tac_plus.cfg

EXPOSE 49
ENTRYPOINT ["/entrypoint.sh"]