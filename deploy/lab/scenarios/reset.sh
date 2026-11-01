#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Kill every application session in the lab, ending all staged incidents.
source "$(dirname "$0")/_lib.sh"

ids=$(root_sql "SELECT ID FROM performance_schema.processlist WHERE USER = 'app'")
for id in $ids; do
  root_sql "KILL $id" 2>/dev/null || true
done
root_sql "TRUNCATE performance_schema.events_statements_summary_by_digest"
echo "reset: killed $(echo "$ids" | grep -c . || true) app session(s), cleared statement digests"
