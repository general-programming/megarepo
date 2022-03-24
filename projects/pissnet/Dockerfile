FROM alpine:3.15

# Basic setup.
RUN apk add --no-cache openssl pcre2 c-ares argon2-dev libsodium && \
    apk add --no-cache -t build-deps openssl-dev pcre2-dev c-ares-dev libsodium-dev git alpine-sdk && \
    git clone https://github.com/unrealircd/unrealircd.git && \
    cd unrealircd/ && \
    sed -i 's/test "x$enable_dynamic_/test "$enable_dynamic_/' ./configure && \
	./configure \
		--prefix=/usr \
		--enable-dynamic-linking \
		--enable-ssl \
		--with-bindir=/usr/bin \
		--with-cachedir=/var/lib/unrealircd/cache \
		--with-confdir=/etc/unrealircd \
		--with-datadir=/var/lib/unrealircd/data \
		--with-docdir=/usr/share/man/unrealircd \
		--with-logdir=/var/log/unrealircd \
		--with-modulesdir=/usr/lib/unrealircd/modules \
		--with-nick-history=2000 \
		--with-permissions=0600 \
		--with-pidfile=/var/lib/unrealircd/run/unrealircd.pid \
		--with-privatelibdir=/usr/lib/unrealircd/private_lib \
		--with-scriptdir=/usr/lib/unrealircd/scripts \
		--with-tmpdir=/var/lib/unrealircd/tmp \
		--with-controlfile=/tmp/unrealircd.ctl && \
    make -j8 && \
    mkdir -p /usr/lib/unrealircd/scripts /usr/lib/unrealircd/run /usr/lib/unrealircd/private_lib && \
    make install && \
    cd .. && rm -rf unrealircd && \
    apk del build-deps && \
    mkdir -p /var/lib/unrealircd/data /var/lib/unrealircd/cache && \
    adduser -D -h /var/lib/unrealircd unrealircd && \
    chown -Rv unrealircd:unrealircd /var/lib/unrealircd /etc/unrealircd /usr/lib/unrealircd && \
    chmod -Rv a+rx /usr/bin

USER unrealircd

CMD ["unrealircd", "-F"]
