// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// The only MySQL the eval will touch: the local lab container from
// lab/docker-compose.yml, published on loopback.
const (
	labContainer = "yuanfang-lab-mysql"
	labAddr      = "127.0.0.1:13306"
	labEnvFile   = "lab/.env"
)

// Lab is a verified connection to the lab MySQL. Root is the eval's own view
// (readiness checks, cleanup); App connects as the application account that
// scenarios use to cause incidents. Yuanfang gets neither: it runs as a
// separate process with the least-privilege yuanfang account.
type Lab struct {
	env  map[string]string
	Root *sql.DB
	App  *sql.DB
}

// OpenLab reads lab/.env and checks that the container really is the lab
// before opening any connection.
func OpenLab(ctx context.Context) (*Lab, error) {
	env, err := readEnvFile(labEnvFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w (run make lab-up first)", labEnvFile, err)
	}
	for _, k := range []string{"LAB_ROOT_PASSWORD", "LAB_YUANFANG_PASSWORD", "LAB_APP_PASSWORD"} {
		if env[k] == "" {
			return nil, fmt.Errorf("%s: %s is missing", labEnvFile, k)
		}
	}
	if err := checkPublishedPort(ctx); err != nil {
		return nil, err
	}
	// Restarts and KILL cut connections on purpose; the driver would log
	// each one. Errors still surface through the calls.
	_ = mysql.SetLogger(log.New(io.Discard, "", 0))
	l := &Lab{env: env}
	if l.Root, err = sql.Open("mysql", l.dsn("root", env["LAB_ROOT_PASSWORD"], "")); err != nil {
		return nil, err
	}
	if l.App, err = sql.Open("mysql", l.dsn("app", env["LAB_APP_PASSWORD"], "shop")); err != nil {
		return nil, err
	}
	// A connection handed back to the pool is closed, never reused: incident
	// sessions may end with state (an open transaction) nobody else should get.
	l.App.SetMaxIdleConns(0)
	if err := l.checkIdentity(ctx); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Close releases the pools.
func (l *Lab) Close() {
	if l.Root != nil {
		_ = l.Root.Close()
	}
	if l.App != nil {
		_ = l.App.Close()
	}
}

// YuanfangDSN is the DSN handed to the Yuanfang process for the lab target.
func (l *Lab) YuanfangDSN() string {
	return l.dsn("yuanfang", l.env["LAB_YUANFANG_PASSWORD"], "")
}

func (l *Lab) dsn(user, pass, db string) string {
	c := mysql.NewConfig()
	c.User, c.Passwd, c.Net, c.Addr, c.DBName = user, pass, "tcp", labAddr, db
	c.Timeout = 5 * time.Second
	return c.FormatDSN()
}

// Restart restarts the container so every run starts from a clean server:
// no sessions, no locks, no deadlock history, fresh performance_schema.
func (l *Lab) Restart(ctx context.Context) error {
	if _, err := docker(ctx, "restart", labContainer); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for {
		health, err := docker(ctx, "inspect", "-f", "{{.State.Health.Status}}", labContainer)
		if err == nil && health == "healthy" && l.Root.PingContext(ctx) == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lab did not become healthy after restart (health %q)", health)
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	// Same check as at start: the restarted server must still be the lab.
	if err := checkPublishedPort(ctx); err != nil {
		return err
	}
	return l.checkIdentity(ctx)
}

// checkPublishedPort makes sure the lab address is the container's port and
// only published on loopback.
func checkPublishedPort(ctx context.Context) error {
	out, err := docker(ctx, "port", labContainer, "3306/tcp")
	if err != nil {
		return fmt.Errorf("container %s not found or not running (run make lab-up): %w", labContainer, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != labAddr {
			return fmt.Errorf("container %s publishes 3306 on %q; refusing to run (expected only %s)", labContainer, line, labAddr)
		}
	}
	return nil
}

// checkIdentity compares the server's @@hostname with the container's
// hostname, which Docker sets per container: a different server answering on
// the lab address fails here.
func (l *Lab) checkIdentity(ctx context.Context) error {
	want, err := docker(ctx, "inspect", "-f", "{{.Config.Hostname}}", labContainer)
	if err != nil {
		return err
	}
	var got string
	if err := l.Root.QueryRowContext(ctx, "SELECT @@hostname").Scan(&got); err != nil {
		return fmt.Errorf("connect to lab as root: %w", err)
	}
	if got != want {
		return fmt.Errorf("server on %s reports hostname %q, container %s has %q; refusing to run", labAddr, got, labContainer, want)
	}
	return nil
}

func docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("docker %s: %w: %s", args[0], err, s)
	}
	return s, nil
}

func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // fixed path inside the repository
	if err != nil {
		return nil, err
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("malformed line")
		}
		env[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return env, sc.Err()
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
