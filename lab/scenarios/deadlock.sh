#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Incident: two checkout transactions decrement inventory rows in opposite
# order and deadlock. One of them is rolled back with ERROR 1213.
# Expected verdict: deadlock between inventory updates with inconsistent lock order.
source "$(dirname "$0")/_lib.sh"

app_sql "BEGIN; UPDATE inventory SET stock = stock - 1 WHERE sku = 'SKU-1'; DO SLEEP(2); UPDATE inventory SET stock = stock - 1 WHERE sku = 'SKU-2'; COMMIT;" &
sleep 0.5
app_sql "BEGIN; UPDATE inventory SET stock = stock - 1 WHERE sku = 'SKU-2'; DO SLEEP(2); UPDATE inventory SET stock = stock - 1 WHERE sku = 'SKU-1'; COMMIT;" || true
wait || true
echo "staged: deadlock between two inventory transactions (one error above is expected)"
echo "try:    yuanfang ask \"checkout sometimes fails with a deadlock error\" -t lab"
