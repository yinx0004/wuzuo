// SPDX-License-Identifier: Apache-2.0

package redact

import "testing"

func TestSQL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT * FROM users WHERE email = 'a@b.com' AND id = 42", "SELECT * FROM users WHERE email = ? AND id = ?"},
		{`UPDATE t SET name = "o""brien", score = 3.14 WHERE id IN (1, 2, 3)`, "UPDATE t SET name = ?, score = ? WHERE id IN (?...)"},
		{"SELECT `col1`, t2.c3 FROM t2 WHERE x = 0x1F", "SELECT `col1`, t2.c3 FROM t2 WHERE x = ?"},
		{`INSERT INTO t VALUES ('it\'s', -5)`, "INSERT INTO t VALUES (?, -?)"},
		{"SELECT * FROM orders_2024 LIMIT 10", "SELECT * FROM orders_2024 LIMIT ?"},
	}
	for _, c := range cases {
		if got := SQL(c.in); got != c.want {
			t.Errorf("SQL(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}

func TestSecrets(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CREATE USER u IDENTIFIED BY 'hunter2'", "CREATE USER u IDENTIFIED BY '<redacted>'"},
		{"password=hunter2 host=db", "password=<redacted> host=db"},
		{"mongodb://admin:s3cr3t@db:27017/x", "mongodb://admin:<redacted>@db:27017/x"},
		{"root:pw@tcp(127.0.0.1:3306)/", "root:<redacted>@tcp(127.0.0.1:3306)/"},
		{"Authorization: Bearer abcdefghijklmnop", "Authorization: Bearer <redacted>"},
	}
	for _, c := range cases {
		if got := Secrets(c.in); got != c.want {
			t.Errorf("Secrets(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}
