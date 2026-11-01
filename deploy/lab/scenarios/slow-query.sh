#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Incident: a new code path filters orders by customer_id, which has no index,
# so every call scans 200k rows. Several workers run it in a loop.
# Expected verdict: full table scans on shop.orders; add an index on customer_id.
source "$(dirname "$0")/_lib.sh"

DURATION="${DURATION:-300}"
for w in 1 2 3 4; do
  # shellcheck disable=SC2016 # expanded inside the container
  app_script_bg '
    end=$(( $(date +%s) + $1 ))
    while [ "$(date +%s)" -lt "$end" ]; do
      mysql -uapp shop -e "SELECT COUNT(*), SUM(amount) FROM orders WHERE customer_id = $(( RANDOM % 20000 + 1 )) AND status <> '"'"'refunded'"'"'" >/dev/null
    done' "$DURATION"
done
echo "staged: 4 workers running unindexed lookups for ${DURATION}s"
echo "try:    wuzuo ask \"the orders page got slow, p99 jumped\" -t lab"
