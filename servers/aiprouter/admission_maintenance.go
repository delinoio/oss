// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/delinoio/oss/packages/aiprouter/store"
	"io"
	"strings"
	"time"
)

const serverHelp = `Usage: aiprouter-server [serve|migrate|restore-invalidate|reencrypt]
       aiprouter-server admission-status
       aiprouter-server admission-drain <instance-id>
       aiprouter-server admission-reconcile <instance-id> --upstreams-stopped

admission-status prints safe instance metadata as JSON.
admission-drain fences new admissions and writes a one-use recovery proof to stdout.
Capture that proof directly in a new private file; do not put it in logs or argv.
admission-reconcile reads the proof from stdin. Use it only after independently
confirming that the selected process AND all its upstream executions have stopped.
It releases only that boot instance's slots and never revokes unrelated keys.
No admission is reclaimed merely because time has elapsed.
`

func validateServerCommand(command string, args []string) error {
	switch command {
	case "serve", "migrate", "restore-invalidate", "reencrypt", "admission-status":
		if len(args) == 0 {
			return nil
		}
	case "admission-drain":
		if len(args) == 1 && validInstanceArg(args[0]) {
			return nil
		}
	case "admission-reconcile":
		if len(args) == 2 && validInstanceArg(args[0]) && args[1] == "--upstreams-stopped" {
			return nil
		}
	}
	return errors.New("invalid server command or options; use --help")
}
func validInstanceArg(v string) bool {
	return v != "" && len(v) <= 512 && !strings.ContainsAny(v, " \t\r\n") && !strings.HasPrefix(v, "-")
}

type admissionMaintenanceStore interface {
	ListAdmissionInstances(context.Context) ([]store.AdmissionInstance, error)
	DrainAdmissionInstance(context.Context, string) (string, error)
	ReconcileTerminatedInstance(context.Context, string, string, bool) (int64, error)
}

func admissionMaintenance(ctx context.Context, s admissionMaintenanceStore, command string, args []string, input io.Reader, output io.Writer) error {
	if e := validateServerCommand(command, args); e != nil {
		return e
	}
	switch command {
	case "admission-status":
		rows, e := s.ListAdmissionInstances(ctx)
		if e != nil {
			return errors.New("admission status unavailable")
		}
		type row struct {
			ID               string    `json:"id"`
			State            string    `json:"state"`
			CreatedAt        time.Time `json:"created_at"`
			ActiveAdmissions int64     `json:"active_admissions"`
		}
		out := make([]row, 0, len(rows))
		for _, i := range rows {
			out = append(out, row{i.ID, i.State, i.CreatedAt, i.ActiveAdmissions})
		}
		return json.NewEncoder(output).Encode(out)
	case "admission-drain":
		proof, e := s.DrainAdmissionInstance(ctx, args[0])
		if e != nil {
			return errors.New("admission drain failed")
		}
		if _, e = fmt.Fprintln(output, proof); e != nil {
			return errors.New("recovery proof output failed; drain again to obtain a new proof")
		}
		return nil
	case "admission-reconcile":
		raw, e := io.ReadAll(io.LimitReader(input, 4097))
		if e != nil || len(raw) > 4096 {
			return errors.New("invalid recovery proof input")
		}
		proof := strings.TrimSpace(string(raw))
		if proof == "" || strings.ContainsAny(proof, " \t\r\n") {
			return errors.New("invalid recovery proof input")
		}
		released, e := s.ReconcileTerminatedInstance(ctx, args[0], proof, true)
		if e != nil {
			return errors.New("admission reconciliation refused")
		}
		return json.NewEncoder(output).Encode(struct {
			ID       string `json:"instance_id"`
			Released int64  `json:"released_admissions"`
		}{args[0], released})
	}
	return errors.New("unknown admission command")
}
