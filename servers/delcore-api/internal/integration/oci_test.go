//go:build oci

// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOCI(t *testing.T) {
	root, e := filepath.Abs("../../../..")
	if e != nil {
		t.Fatal(e)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("docker", args...)
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("docker %s: %v\n%s", args[0], e, b)
		}
		return strings.TrimSpace(string(b))
	}
	mustFail := func(args ...string) {
		t.Helper()
		if b, e := exec.Command("docker", args...).CombinedOutput(); e == nil {
			t.Fatalf("expected failure: %s", b)
		}
	}
	image := "delcore-api:validation"
	if user := run("image", "inspect", "--format", "{{.Config.User}}", image); user != "65532:65532" {
		t.Fatal("image must run as nonroot", user)
	}
	name := "delcore-oci-" + strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	run("run", "-d", "--name", name, "-e", "POSTGRES_PASSWORD=fixture-only", "-e", "POSTGRES_DB=delcore", "postgres:15-bookworm")
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	ready := false
	for i := 0; i < 60; i++ {
		if exec.Command("docker", "exec", name, "pg_isready", "-U", "postgres").Run() == nil {
			ready = true
			break
		}
		time.Sleep(time.Second)
	}
	if !ready {
		t.Fatal("PostgreSQL did not start")
	}
	dir := t.TempDir()
	if e = os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	arch := run("image", "inspect", "--format", "{{.Architecture}}", "postgres:15-bookworm")
	binary := filepath.Join(dir, "provider")
	cmd := exec.Command("go", "build", "-o", binary, "./servers/delcore-api/internal/testprovider")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture build: %v %s", e, b)
	}
	run("cp", binary, name+":/tmp/provider")
	run("exec", "-d", name, "/tmp/provider")
	raw, e := os.ReadFile(filepath.Join(root, "servers/delcore-api/config.example.json"))
	if e != nil {
		t.Fatal(e)
	}
	var cfg map[string]any
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	cfg["database_url"] = "postgres://postgres:fixture-only@127.0.0.1:5432/delcore?sslmode=disable"
	cfg["issuer"] = "http://127.0.0.1:8099"
	cfg["listen"] = "127.0.0.1:8080"
	cfg["timeout_seconds"] = 5
	raw, e = json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	configPath := filepath.Join(dir, "config.json")
	if e = os.WriteFile(configPath, raw, 0644); e != nil {
		t.Fatal(e)
	}
	args := []string{"run", "--rm", "--network", "container:" + name, "-v", dir + ":/fixture:ro", image, "-config", "/fixture/config.json"}
	mustFail("run", "--rm", image) // Missing configuration.
	mustFail(args...)              // Empty schema must never be migrated by normal startup.
	run(append(args, "-migrate")...)
	run("exec", name, "psql", "-U", "postgres", "-d", "delcore", "-c", "UPDATE delcore_schema SET version=999 WHERE version=4")
	mustFail(args...)
	mustFail(append(args, "-migrate")...)
	run("exec", name, "psql", "-U", "postgres", "-d", "delcore", "-c", "UPDATE delcore_schema SET version=4 WHERE version=999")
	app := name + "-app"
	run("run", "-d", "--name", app, "--network", "container:"+name, "-v", dir+":/fixture:ro", image, "-config", "/fixture/config.json")
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", app).Run() })
	started := false
	for i := 0; i < 30; i++ {
		if strings.Contains(run("logs", app), "delcore started") {
			started = true
			break
		}
		time.Sleep(time.Second)
	}
	if !started {
		t.Fatal("application did not start")
	}
	mustFail(args...) // The database lock rejects another instance.
	run("stop", "--time", "15", app)
	if code := run("inspect", "--format", "{{.State.ExitCode}}", app); code != "0" {
		t.Fatal("unclean shutdown", code)
	}
	// Restore a real logical backup into a separate database and migrate/check it.
	run("exec", name, "pg_dump", "-U", "postgres", "-d", "delcore", "-Fc", "-f", "/tmp/backup.dump")
	run("exec", name, "createdb", "-U", "postgres", "restored")
	run("exec", name, "pg_restore", "-U", "postgres", "-d", "restored", "/tmp/backup.dump")
	cfg["database_url"] = "postgres://postgres:fixture-only@127.0.0.1:5432/restored?sslmode=disable"
	raw, e = json.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(configPath, raw, 0644); e != nil {
		t.Fatal(e)
	}
	run(append(args, "-migrate")...)
	if version := run("exec", name, "psql", "-U", "postgres", "-d", "restored", "-Atc", "SELECT max(version) FROM delcore_schema"); version != "4" {
		t.Fatal("restored schema", version)
	}
}
