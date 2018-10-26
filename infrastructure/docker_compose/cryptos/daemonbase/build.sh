set -x 
shopt -s nocaseglob

cd /tmp/code

# Actual builders
prepare () {
    # Fixes container caveats if we find any in the future.
    true
}

build_autogen () {
    ./autogen.sh
    ./configure \
        --prefix="${COIN_PREFIX}" \
        --mandir=/usr/share/man \
        --disable-tests \
        --disable-bench \
        --disable-ccache \
        --with-gui=no \
        --with-utils \
        --with-libs \
        --with-daemon
    make -j8
    make install
}

build_makefile () {
    cd src/
    USE_UPNP=- make -f makefile.unix -j8

    # Do a manual install if the install gives a bad status code.
    # Something like `make: *** No rule to make target 'install'.  Stop.`
    PREFIX="${COIN_PREFIX}" make -f makefile.unix -j8 install
    LASTCODE=$?

    if [ $LASTCODE -ne 0 ]; then
        mkdir -p "${COIN_PREFIX}/bin"

        # Install the daemon/cli binaries.
        if [ ! -f "${COIN_NAME}-cli" ]; then
            # Terrible hack if the daemon was made before the coind/coind-cli split.
            mv "${COIN_NAME}d" "${COIN_PREFIX}/bin/${COIN_NAME}d"
            mv "${COIN_NAME}d" "${COIN_PREFIX}/bin/${COIN_NAME}-cli"
        else
            mv "${COIN_NAME}-cli" "${COIN_NAME}d" "${COIN_PREFIX}/bin/"
        fi

        # Install the tx binary if it exists.
        if [ -f "${COIN_NAME}-tx" ]; then
            mv "${COIN_NAME}-tx" "${COIN_PREFIX}/bin/"
        fi
    fi
}

rename_daemons() {
    cd "${COIN_PREFIX}/bin"
    # Terrible assumption that only a single file would be suffixed -cli, -tx, d
    mv *-cli coin-cli
    mv *-tx coin-tx
    mv *d coind
}

# Technically our main function.
if [ -f autogen.sh ]; then
    prepare
    build_autogen
    rename_daemons
fi

if [ -f src/makefile.unix ]; then
    prepare
    build_makefile
    rename_daemons
fi
