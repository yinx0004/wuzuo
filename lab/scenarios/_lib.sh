#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Helpers for staging incidents in the WuZuo lab container. Lab only.
#
# Credentials are never passed from here: commands run inside the container
# and read the passwords from its environment. SQL is passed as a positional
# argument, so it is never re-parsed by a shell.
set -euo pipefail

LAB_CONTAINER="${LAB_CONTAINER:-wuzuo-lab-mysql}"

# app_sql "<statements>" — run statements as the application user, foreground.
app_sql() {
  docker exec -i "$LAB_CONTAINER" sh -c 'MYSQL_PWD="$LAB_APP_PASSWORD" exec mysql -uapp shop -e "$1"' sh "$1"
}

# app_session_bg "<statements>" [seconds] — open a background session that runs
# the statements and then stays connected (idle) for the given seconds.
app_session_bg() {
  docker exec -d "$LAB_CONTAINER" \
    sh -c '(echo "$1"; sleep "$2") | MYSQL_PWD="$LAB_APP_PASSWORD" mysql -uapp shop' sh "$1" "${2:-600}"
}

# app_query_bg "<statement>" — run one statement in the background (it may block).
app_query_bg() {
  docker exec -d "$LAB_CONTAINER" sh -c 'MYSQL_PWD="$LAB_APP_PASSWORD" exec mysql -uapp shop -e "$1"' sh "$1"
}

# app_script_bg '<shell script>' [args...] — run a shell script in the container
# in the background with MYSQL_PWD set for the application user.
app_script_bg() {
  local script="$1"
  shift
  docker exec -d "$LAB_CONTAINER" sh -c 'export MYSQL_PWD="$LAB_APP_PASSWORD"; '"$script" sh "$@"
}

root_sql() {
  docker exec -i "$LAB_CONTAINER" sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot -N -e "$1"' sh "$1"
}
