package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

const doctorInventoryLimit = 50

func (s *Service) GetDoctor(ctx context.Context, req *connect.Request[pb.GetDoctorRequest]) (*connect.Response[pb.GetDoctorResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	report, err := s.doctor(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	if len(raw) > 1<<20 {
		return nil, rpc.Error(domain.Fail(domain.ResourceExhausted, "The diagnostic report exceeds its bound.", "Inspect individual Workers and accounts."), req.Header().Get(rpc.CorrelationHeader))
	}
	s.logger.Info("doctor_observed", "correlation_id", req.Header().Get(rpc.CorrelationHeader), "storage_state", report.Storage.Result.State, "machines", len(report.Machines), "credentials", len(report.Credentials), "more_machines", report.MoreMachines, "more_credentials", report.MoreCredentials)
	response := connect.NewResponse(&pb.GetDoctorResponse{ReportJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) doctor(ctx context.Context) (domain.DoctorReport, error) {
	report := domain.DoctorReport{SchemaVersion: 2, Version: rpc.Version, ProtocolVersion: rpc.ProtocolVersion, DatabaseSchemaVersion: store.SchemaVersion, OS: runtime.GOOS, Architecture: runtime.GOARCH, ServerID: s.Identity.ServerID, Listener: s.Endpoint.URL, Database: "failed", CredentialStore: "owner-credential-ready", Machines: []domain.DiagnosticMachine{}, Credentials: []domain.DiagnosticCredential{}}
	var err error
	report.Storage, err = s.Store.DiagnosticStorage(ctx)
	if err != nil {
		report.Storage.Result = doctorFailure(err, "Inspect the server's private storage permissions, database and free disk space; no repair was performed.")
	}
	if report.Storage.LogicalDatabaseBytes != nil {
		report.Database = "ready"
	}
	var machines, accounts []store.Record
	// This snapshot and the final authorization check are separate from native I/O.
	// A revoked caller must not receive a report, including a partial failure report.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		machines, err = tx.List(store.Filter{Kind: domain.MachineKind, Limit: doctorInventoryLimit + 1})
		if err != nil {
			return err
		}
		accounts, err = tx.List(store.Filter{Kind: domain.AccountKind, Limit: doctorInventoryLimit + 1})
		return err
	})
	if err != nil {
		return report, err
	}
	if len(machines) > doctorInventoryLimit {
		report.MoreMachines = true
		machines = machines[:doctorInventoryLimit]
	}
	if len(accounts) > doctorInventoryLimit {
		report.MoreCredentials = true
		accounts = accounts[:doctorInventoryLimit]
	}
	s.connectionsMu.Lock()
	active := make(map[domain.ID]bool, len(s.workerStreams))
	for id := range s.workerStreams {
		active[id] = true
	}
	s.connectionsMu.Unlock()
	for _, record := range machines {
		machine, err := store.Decode[domain.Machine](record)
		if err != nil {
			return report, err
		}
		report.Machines = append(report.Machines, doctorMachine(record.ID, machine, active[record.ID]))
	}
	for _, record := range accounts {
		observation, err := s.doctorCredential(ctx, record.ID)
		if err != nil {
			return report, err
		}
		report.Credentials = append(report.Credentials, observation)
	}
	// Native inspection can race peer edits or revocation. Publish each observation
	// only for the original connection, never relabel an old result as a new one.
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		for index := range report.Credentials {
			result := &report.Credentials[index]
			record, err := tx.Get(domain.AccountKind, result.AccountID)
			if err != nil {
				if domain.SafeError(err).Code != domain.NotFound {
					return err
				}
				result.Result = doctorSuperseded()
				continue
			}
			account, err := store.Decode[domain.Account](record)
			if err != nil {
				return err
			}
			current := domain.ID("")
			if account.Connection != nil {
				current = account.Connection.ID
			}
			if current != result.ConnectionID {
				result.Result = doctorSuperseded()
			}
		}
		return nil
	})
	report.ObservedAt = time.Now().UTC()
	return report, err
}

func doctorFailure(err error, guidance string) domain.DiagnosticResult {
	code := domain.SafeError(err).Code
	state := domain.DiagnosticFailed
	switch code {
	case domain.Unavailable, domain.ServerUnavailable, domain.Unsupported, domain.Canceled:
		state = domain.DiagnosticUnavailable
	}
	// Native/filesystem error messages and guidance are untrusted. Return only the
	// closed code and the recovery instruction owned by this diagnostic boundary.
	return domain.DiagnosticResult{State: state, Code: code, Guidance: guidance}
}
func doctorSuperseded() domain.DiagnosticResult {
	return domain.DiagnosticResult{State: domain.DiagnosticSuperseded, Code: domain.Conflict, Guidance: "The account connection changed during inspection. Refresh diagnostics for its current connection."}
}
func (s *Service) doctorCredential(ctx context.Context, id domain.ID) (domain.DiagnosticCredential, error) {
	result := domain.DiagnosticCredential{AccountID: id}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return result, err
	}
	defer unlock()
	var account domain.Account
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		record, err := tx.Get(domain.AccountKind, id)
		if err != nil {
			return err
		}
		account, err = store.Decode[domain.Account](record)
		return err
	})
	if err != nil {
		if domain.SafeError(err).Code == domain.NotFound {
			result.Result = doctorSuperseded()
			return result, nil
		}
		return result, err
	}
	if account.Connection != nil {
		result.ConnectionID = account.Connection.ID
	}
	if account.Type == domain.SubscriptionAccount {
		result.Result = domain.DiagnosticResult{State: domain.DiagnosticUnavailable, Code: domain.Unsupported, Guidance: "Subscription credential inspection is unavailable. No native login was read or changed."}
		return result, nil
	}
	if account.Connection == nil {
		result.Result = domain.DiagnosticResult{State: domain.DiagnosticUnconfigured, Guidance: "This account has no active connection. Use AI accounts to connect it explicitly."}
		return result, nil
	}
	if account.Connection.Authentication == domain.KeylessAuth {
		result.Result = domain.DiagnosticResult{State: domain.DiagnosticNotApplicable, Guidance: "This connection is keyless; no credential store access is required."}
		return result, nil
	}
	reader := s.accountSecrets
	if reader == nil {
		// Opening the normal vault initializes and reconciles state. Doctor must use
		// only the existing private scope and close its temporary inspection lock.
		vault, err := credentials.OpenExisting(filepath.Join(s.Store.Root(), "secrets"), s.Identity.ServerID, s.logger)
		if err != nil {
			result.Result = doctorFailure(err, "Inspect the existing server credential scope and OS store permissions. Unlock it yourself if required; do not replace missing keys or original state.")
			return result, nil
		}
		defer vault.Close()
		reader = vault
	}
	secret, err := reader.Get(ctx, credentials.Ref{Owner: id, ID: account.Connection.ID, Purpose: credentials.AccountAPI})
	clear(secret)
	if err != nil {
		result.Result = doctorFailure(err, "Inspect this original account connection and the server OS credential store. Unlock it yourself if required; do not recreate missing protected keys.")
	} else {
		result.Result = domain.DiagnosticResult{State: domain.DiagnosticObserved, Guidance: "The exact saved credential was readable. Provider authentication, execution readiness and quota were not checked."}
	}
	return result, nil
}

func doctorMachine(id domain.ID, machine domain.Machine, active bool) domain.DiagnosticMachine {
	result := domain.DiagnosticMachine{MachineID: id, Name: machine.Name, OS: machine.OS, Architecture: machine.Architecture, Version: machine.Version, LastSeen: machine.LastSeen, Disabled: machine.Disabled, ActiveStream: active, Installations: []domain.DiagnosticInstallation{}}
	for _, harness := range domain.Harnesses() {
		value := domain.DiagnosticInstallation{Harness: harness, State: domain.InstallationUnchecked, Capabilities: []domain.Capability{}, Guidance: "Refresh discovery on this Worker to observe its selected installation."}
		for _, installation := range machine.Installations {
			if installation.Harness != harness {
				continue
			}
			value.State, value.Version, value.ObservedAt = installation.State, installation.Version, installation.ObservedAt
			value.Capabilities = append(value.Capabilities, installation.Capabilities...)
			value.ProtocolVerified = installation.ProtocolVerified
			value.Guidance = "Retained version discovery does not establish current account execution readiness. Refresh discovery for a new observation."
			problem := domain.InstallationProblem(installation.State)
			if installation.Protocol != nil {
				value.ProtocolState = installation.Protocol.State
				if protocolProblem := domain.ProtocolProblem(value.ProtocolState); protocolProblem != nil {
					problem = protocolProblem
				}
			}
			if problem != nil {
				value.ProblemCode, value.Guidance = problem.Code, problem.Guidance
			}
		}
		result.Installations = append(result.Installations, value)
	}
	return result
}
