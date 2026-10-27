// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/store"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if e := run(logger, os.Args[1:]); e != nil {
		logger.Error("server stopped", "reason", e.Error())
		os.Exit(1)
	}
}
func run(logger *slog.Logger, args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
	}
	if command == "--help" || command == "-h" || command == "help" {
		_, e := fmt.Fprint(os.Stdout, serverHelp)
		return e
	}
	options := []string{}
	if len(args) > 1 {
		options = args[1:]
	}
	if e := validateServerCommand(command, options); e != nil {
		return e
	}
	c, e := loadConfig(os.Getenv)
	if e != nil {
		return e
	}
	keys, e := core.NewEnvelope(c.ActiveKey, c.EncryptionKeys)
	if e != nil {
		return errors.New("encryption configuration failed")
	}
	operators := []core.Identity{}
	for _, i := range c.Operators {
		operators = append(operators, core.Identity{Issuer: i.Issuer, Subject: i.Subject})
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db, e := store.Open(startup, c.DatabaseURL, store.Options{Keys: keys, Operators: operators})
	if e != nil {
		return errors.New("database connection failed")
	}
	defer db.Close()
	if command != "serve" {
		switch command {
		case "admission-status", "admission-drain", "admission-reconcile":
			e = admissionMaintenance(ctx, db, command, options, os.Stdin, os.Stdout)
		case "migrate":
			e = db.Migrate(ctx)
		case "restore-invalidate":
			e = db.InvalidateAfterRestore(ctx)
		case "reencrypt":
			e = db.RotateEncryption(ctx)
		}
		if e != nil {
			return errors.New("maintenance failed; service remains offline")
		}
		logger.Info("maintenance completed", "command", command)
		return nil
	}
	if e = db.RegisterAdmissionInstance(startup); e != nil {
		return errors.New("admission instance registration failed")
	}
	auth, e := newAuth(startup, c, persistentAuth{s: db})
	if e != nil {
		return e
	}
	shutdownTrace, e := setupTracing(startup, os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "")
	if e != nil {
		return errors.New("trace exporter initialization failed")
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTrace(c)
	}()
	objects, e := artifacts.NewR2(artifacts.R2Config{Endpoint: c.R2Endpoint, Bucket: c.R2Bucket, AccessKeyID: c.R2AccessKeyID, SecretAccessKey: c.R2SecretAccessKey})
	if e != nil {
		return errors.New("R2 configuration failed")
	}
	artifactService := artifacts.New(db, objects)
	telemetry := newTelemetry(logger)
	releases := newAdmissionReleaseQueue(context.Background(), db, telemetry)
	defer releases.Close()
	service := &server{config: c, store: db, auth: auth, telemetry: telemetry, artifacts: artifactService, releases: releases}
	if c.ConsoleDir != "" {
		service.console, e = consoleHandler(c.ConsoleDir)
		if e != nil {
			return errors.New("console artifact unavailable")
		}
	}
	listener, e := net.Listen("tcp", c.ListenAddr)
	if e != nil {
		return errors.New("server listen failed")
	}
	httpServer := &http.Server{Handler: service.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 64 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
	workers, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	var workerWG sync.WaitGroup
	workerWG.Add(2)
	go func() { defer workerWG.Done(); service.cleanup(workers) }()
	go func() { defer workerWG.Done(); service.mailWorker(workers) }()
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	logger.Info("server listening", "address", c.ListenAddr, "instance_id", db.InstanceID())
	select {
	case e := <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			return errors.New("HTTP server failed")
		}
	case <-ctx.Done():
	}
	stopWorkers()
	telemetry.health.Set(0)
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	if e = httpServer.Shutdown(shutdown); e != nil {
		_ = httpServer.Close()
		return errors.New("graceful shutdown deadline exceeded")
	}
	finished := make(chan struct{})
	go func() { workerWG.Wait(); service.sockets.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-shutdown.Done():
		return errors.New("worker shutdown deadline exceeded")
	}
	if e = releases.Wait(shutdown); e != nil {
		return errors.New("completed admission releases require offline reconciliation")
	}
	logger.Info("server stopped")
	return nil
}
