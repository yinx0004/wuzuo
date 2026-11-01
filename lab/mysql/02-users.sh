#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Creates the lab accounts. Passwords come from the container environment,
# which `make lab-up` fills from the generated, gitignored lab/.env.
# The WuZuo account mirrors docs/mysql-privileges.sql.
set -euo pipefail

: "${LAB_WUZUO_PASSWORD:?missing; run make lab-up}"
: "${LAB_APP_PASSWORD:?missing; run make lab-up}"

MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --protocol=socket -uroot <<SQL
CREATE USER 'wuzuo'@'%' IDENTIFIED BY '${LAB_WUZUO_PASSWORD}';
GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'wuzuo'@'%';
GRANT SELECT ON performance_schema.* TO 'wuzuo'@'%';
GRANT SELECT, EXECUTE ON sys.* TO 'wuzuo'@'%';
-- Optional grant so table stats and EXPLAIN work in the lab.
GRANT SELECT, SHOW VIEW ON shop.* TO 'wuzuo'@'%';

CREATE USER 'app'@'%' IDENTIFIED BY '${LAB_APP_PASSWORD}';
GRANT SELECT, INSERT, UPDATE, DELETE ON shop.* TO 'app'@'%';
SQL
