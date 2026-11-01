-- SPDX-License-Identifier: Apache-2.0
--
-- Least-privilege account for WuZuo on MySQL 8.0+.
-- WuZuo only reads diagnostics; this account cannot read application rows
-- unless you grant the optional privileges at the bottom.

-- MySQL generates a strong password and returns it once (8.0.18+). Store it
-- in your secret manager and expose it to WuZuo through dsn_env.
CREATE USER IF NOT EXISTS 'wuzuo'@'%' IDENTIFIED BY RANDOM PASSWORD;

-- See every session, INNODB_TRX and SHOW ENGINE INNODB STATUS.
GRANT PROCESS ON *.* TO 'wuzuo'@'%';
-- SHOW REPLICA STATUS / SHOW BINARY LOG STATUS.
GRANT REPLICATION CLIENT ON *.* TO 'wuzuo'@'%';
-- Statement digests, locks, error log, status and variables.
GRANT SELECT ON performance_schema.* TO 'wuzuo'@'%';
-- sys views (lock waits) and the helper functions they call.
GRANT SELECT, EXECUTE ON sys.* TO 'wuzuo'@'%';

-- Optional, per application schema: needed by mysql_table_stats (to see the
-- schema in information_schema) and mysql_explain (EXPLAIN requires SELECT;
-- the probe itself is enabled with safety.allow_explain).
-- This grants read access to application data; WuZuo's probes never select
-- application rows, but the account could.
-- GRANT SELECT, SHOW VIEW ON `app_schema`.* TO 'wuzuo'@'%';
