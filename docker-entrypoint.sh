#!/bin/sh
set -eu

chown -R passport:passport /data
exec su-exec passport /usr/local/bin/lunapassport "$@"
