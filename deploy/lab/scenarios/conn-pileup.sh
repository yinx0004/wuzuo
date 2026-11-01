#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Incident: a connection pool leak leaves dozens of idle connections open,
# pushing the server towards max_connections (60 in the lab).
# Expected verdict: connection exhaustion by idle 'app' sessions.
source "$(dirname "$0")/_lib.sh"

N="${N:-50}"
# shellcheck disable=SC2016 # expanded inside the container
app_script_bg 'for i in $(seq 1 "$1"); do (echo "SELECT 1;"; sleep 900) | mysql -uapp shop & done; wait' "$N"
echo "staged: $N idle application connections (max_connections=60)"
echo "try:    wuzuo ask \"new requests fail with too many connections\" -t lab"
