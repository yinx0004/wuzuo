-- SPDX-License-Identifier: Apache-2.0
-- Seed data for the WuZuo lab. Runs once when the container is created.

CREATE DATABASE IF NOT EXISTS shop;
USE shop;

CREATE TABLE customers (
  id         BIGINT PRIMARY KEY AUTO_INCREMENT,
  email      VARCHAR(255) NOT NULL,
  region     VARCHAR(16)  NOT NULL,
  created_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;

-- Deliberately missing an index on (customer_id) and on (status, created_at).
CREATE TABLE orders (
  id          BIGINT PRIMARY KEY AUTO_INCREMENT,
  customer_id BIGINT        NOT NULL,
  status      VARCHAR(16)   NOT NULL,
  amount      DECIMAL(10,2) NOT NULL,
  note        VARCHAR(255)  NULL,
  created_at  DATETIME      NOT NULL
) ENGINE=InnoDB;

CREATE TABLE inventory (
  sku   VARCHAR(32) PRIMARY KEY,
  stock INT NOT NULL
) ENGINE=InnoDB;

SET SESSION cte_max_recursion_depth = 300000;

INSERT INTO customers (email, region, created_at)
WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 20000)
SELECT CONCAT('user', n, '@example.com'), ELT(1 + n % 4, 'cn-north', 'cn-east', 'sg', 'us-west'),
       NOW() - INTERVAL n MINUTE
FROM seq;

INSERT INTO orders (customer_id, status, amount, note, created_at)
WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM seq WHERE n < 200000)
SELECT 1 + n % 20000, ELT(1 + n % 5, 'new', 'paid', 'shipped', 'done', 'refunded'),
       ROUND(10 + (n % 997) * 1.37, 2), CONCAT('gift wrap for order ', n),
       NOW() - INTERVAL n SECOND
FROM seq;

INSERT INTO inventory VALUES ('SKU-1', 100), ('SKU-2', 100), ('SKU-3', 100);
