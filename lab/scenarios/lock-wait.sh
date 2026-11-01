#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Incident: an application session updates a hot row and then goes idle without
# committing. Every later update of that row queues behind it.
# Expected verdict: idle-in-transaction session holding a row lock on shop.orders.
source "$(dirname "$0")/_lib.sh"

app_session_bg "BEGIN; UPDATE orders SET status = 'hold', note = 'manual fix by ops' WHERE id = 42;" 900
sleep 1
for i in 1 2 3 4 5; do
  app_query_bg "UPDATE orders SET amount = amount + $i WHERE id = 42"
done
echo "staged: 1 idle transaction holding orders.id=42, 5 blocked updates"
echo "try:    yuanfang ask \"updates to orders are hanging since a minute ago\" -t lab"
