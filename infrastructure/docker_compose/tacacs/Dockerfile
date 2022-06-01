FROM alpine:latest as base
MAINTAINER David Chidell (dchidell@cisco.com)
ENV VERSION=202205242004
ENV TAC_PLUS_BIN=/tacacs/sbin/tac_plus


FROM base as build
RUN apk add --no-cache \
    build-base bzip2 perl perl-digest-md5 perl-ldap
ADD https://www.pro-bono-publico.de/projects/src/DEVEL.$VERSION.tar.bz2 /tac_plus.tar.bz2
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

EXPOSE 49
CMD ["/tacacs/sbin/tac_plus", "-f", "/tac_plus.conf"]
