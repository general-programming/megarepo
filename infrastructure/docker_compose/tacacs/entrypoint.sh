#!/bin/sh

# Check configuration file exists
if [ ! -f /etc/tac_plus/tac_plus.cfg ]; then
    echo "No configuration file at ${CONF_FILE}"
    exit 1
fi

# Make the log directories
mkdir -p /var/log/tac_plus

echo "Starting server..."

# Start the server
exec ${TAC_PLUS_BIN} -d 256 -f ${CONF_FILE}
