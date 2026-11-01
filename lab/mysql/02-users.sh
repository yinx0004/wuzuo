#!/bin/bash
# SPDX-License-Identifier: Apache-2.0
# Creates the lab accounts. Passwords come from the container environment,
# which `make lab-up` fills from the generated, gitignored lab/.env.
# The Yuanfang account mirrors docs/mysql-privileges.sql.
set -euo pipefail

: "${LAB_YUANFANG_PASSWORD:?missing; run make lab-up}"
: "${LAB_APP_PASSWORD:?missing; run make lab-up}"

MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --protocol=socket -uroot <<SQL
CREATE USER 'yuanfang'@'%' IDENTIFIED BY '${LAB_YUANFANG_PASSWORD}';
GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'yuanfang'@'%';
GRANT SELECT ON performance_schema.* TO 'yuanfang'@'%';
GRANT SELECT, EXECUTE ON sys.* TO 'yuanfang'@'%';
-- Optional grant so table stats and EXPLAIN work in the lab.
GRANT SELECT, SHOW VIEW ON shop.* TO 'yuanfang'@'%';

CREATE USER 'app'@'%' IDENTIFIED BY '${LAB_APP_PASSWORD}';
GRANT SELECT, INSERT, UPDATE, DELETE ON shop.* TO 'app'@'%';
SQL
