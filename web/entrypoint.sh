#!/bin/sh
set -e
/usr/local/bin/status-api &
exec nginx -g "daemon off;"
