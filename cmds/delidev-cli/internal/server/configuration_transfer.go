package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Dependency order also keeps committed configuration/event publication stable.
var portableKinds = []domain.Kind{domain.ProviderKind, domain.ModelKind, domain.AccountKind, domain.TemplateKind, domain.AgentKind, domain.RepositoryKind, domain.ProjectKind, domain.SettingsKind}

func configurationActor(ctx context.Context) (domain.Principal, error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return actor, domain.Fail(domain.PermissionDenied, "Only an owner or paired client can transfer configuration.", "Use an authorized product client.")
	}
	return actor, nil
}
func transferInvalid() error {
	return domain.Fail(domain.InvalidArgument, "Invalid portable configuration or mapping.", "Use the versioned export, map each machine and checkout explicitly, and preserve every configuration reference.")
}
func transferConflict() error {
	return domain.Fail(domain.Conflict, "The configuration import conflicts with current settings.", "Review the conflict, choose explicit reuse or settings replacement, and generate a new preview.")
}
func transferLimit() error {
	return domain.Fail(domain.ResourceExhausted, "The configuration transfer exceeds its bound.", "Use at most 256 configuration entries, 64 checkouts and a 384 KiB export document.")
}

// Observed readiness/provenance cannot cross a server boundary. In particular,
// even keyless accounts require a fresh explicit connection and validation.
func portableValue(kind domain.Kind, raw []byte, incoming bool) (validatable, error) {
	value, err := configurationValue(kind, raw)
	if err != nil {
		return nil, err
	}
	if kind == domain.ProviderKind {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		if field, present := fields["enabled"]; !present {
			return nil, domain.Fail(domain.InvalidArgument, "Provider availability is required.", "Set enabled explicitly to true or false.")
		} else {
			var enabled bool
			if bytes.Equal(bytes.TrimSpace(field), []byte("null")) || json.Unmarshal(field, &enabled) != nil {
				return nil, domain.Fail(domain.InvalidArgument, "Provider availability must be a boolean.", "Set enabled to true or false.")
			}
		}
		if field, present := fields["preset_id"]; present {
			var preset string
			if bytes.Equal(bytes.TrimSpace(field), []byte("null")) || json.Unmarshal(field, &preset) != nil || !domain.ProviderPresetID(preset).Valid() {
				return nil, domain.Fail(domain.InvalidArgument, "Invalid managed preset identity.", "Use a supported preset identifier or omit preset_id for a custom provider.")
			}
		}
	}
	before, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	switch v := value.(type) {
	case *domain.Provider:
		if v.Protocol == domain.NativeSubscription {
			return nil, domain.Fail(domain.Unsupported, "Provider-bound subscription configuration is unsupported.", "Use a service-native subscription account and model.")
		}
	case *domain.Account:
		v.Subscription = nil
		v.Health = domain.AccountDisconnected
		v.Quota = []domain.QuotaWindow{}
		v.ConfirmedExhausted = false
		v.Connection, v.Removal, v.Validation, v.Catalog = nil, nil, nil, nil
	case *domain.Model:
		v.Manual, v.New, v.Discovery = true, false, nil
		if v.MetadataSource == domain.Known {
			v.MetadataSource = domain.UserDeclared
		}
	case *domain.Repository:
		if v.IntegrationID != "" {
			return nil, domain.Fail(domain.Unsupported, "This repository contains an integration that has no portable authentication mapping.", "Preserve its configuration and configure the integration explicitly before transferring it.")
		}
	}
	after, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if incoming && !bytes.Equal(before, after) {
		return nil, domain.Fail(domain.InvalidArgument, "Portable configuration contains server-owned observations.", "Use a current export; authentication, quota and discovery evidence cannot be imported.")
	}
	return value, nil
}
func portableDocument(kind domain.Kind, raw []byte) (json.RawMessage, error) {
	value, err := portableValue(kind, raw, false)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func configurationSnapshot(tx *store.Tx) (map[domain.ID]store.Record, error) {
	result := map[domain.ID]store.Record{}
	size := 0
	for _, kind := range portableKinds {
		rows, err := all(tx, kind)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			size += len(row.Data)
			if len(result) >= 10000 || size > 16<<20 {
				return nil, transferLimit()
			}
			result[row.ID] = row
		}
	}
	return result, nil
}
func exportConfiguration(tx *store.Tx) (domain.ConfigurationBundle, error) {
	bundle := domain.ConfigurationBundle{Version: domain.ConfigurationBundleVersion, Entries: []domain.ConfigurationEntry{}, Machines: []domain.ConfigurationMachine{}}
	machineIDs := map[domain.ID]bool{}
	size, checkouts := 0, 0
	for _, kind := range portableKinds {
		rows, err := all(tx, kind)
		if err != nil {
			return bundle, err
		}
		for _, row := range rows {
			if len(bundle.Entries) >= domain.MaxConfigurationEntries {
				return bundle, transferLimit()
			}
			value, err := portableValue(kind, row.Data, false)
			if err != nil {
				return bundle, err
			}
			switch v := value.(type) {
			case *domain.Repository:
				if v.Remediation != nil && v.Remediation.MachineID != "" {
					machineIDs[v.Remediation.MachineID] = true
				}
				for _, checkout := range v.Checkouts {
					machineIDs[checkout.MachineID] = true
					checkouts++
				}
			case *domain.Settings:
				if v.Remediation.MachineID != "" {
					machineIDs[v.Remediation.MachineID] = true
				}
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return bundle, err
			}
			size += len(raw)
			if size > domain.MaxConfigurationBundleBytes || checkouts > domain.MaxConfigurationCheckouts || len(machineIDs) > domain.MaxConfigurationCheckouts {
				return bundle, transferLimit()
			}
			bundle.Entries = append(bundle.Entries, domain.ConfigurationEntry{ID: row.ID, Kind: kind, Document: raw})
		}
	}
	ids := make([]domain.ID, 0, len(machineIDs))
	for id := range machineIDs {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		record, err := tx.Get(domain.MachineKind, id)
		if err != nil {
			return bundle, err
		}
		machine, err := store.Decode[domain.Machine](record)
		if err != nil {
			return bundle, err
		}
		bundle.Machines = append(bundle.Machines, domain.ConfigurationMachine{ID: id, Name: machine.Name, OS: machine.OS, Architecture: machine.Architecture})
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return bundle, err
	}
	if len(raw) > domain.MaxConfigurationBundleBytes {
		return bundle, transferLimit()
	}
	return bundle, nil
}

// The overlay runs the existing configuration relationship checks against a
// coherent prospective graph without opening a write transaction or emitting
// preview events. Previous self-values remain available for immutable fields.
type configurationOverlay struct {
	tx      *store.Tx
	current map[domain.ID]store.Record
	staged  map[domain.ID]store.Record
	self    domain.ID
}

func (v *configurationOverlay) Get(kind domain.Kind, id domain.ID) (store.Record, error) {
	if id != v.self {
		if row, ok := v.staged[id]; ok {
			if row.Kind != kind {
				return store.Record{}, transferInvalid()
			}
			return row, nil
		}
	}
	if row, ok := v.current[id]; ok {
		if row.Kind != kind {
			return store.Record{}, transferInvalid()
		}
		return row, nil
	}
	return v.tx.Get(kind, id)
}
func (v *configurationOverlay) List(f store.Filter) ([]store.Record, error) {
	if f.SessionID != "" || f.ProjectID != "" || f.Limit < 1 || f.Limit > store.MaxPage {
		return nil, transferInvalid()
	}
	merged := map[domain.ID]store.Record{}
	for id, r := range v.current {
		merged[id] = r
	}
	for id, r := range v.staged {
		merged[id] = r
	}
	rows := []store.Record{}
	for id, r := range merged {
		if r.Kind == f.Kind && id > f.After {
			rows = append(rows, r)
		}
	}
	slices.SortFunc(rows, func(a, b store.Record) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
	}
	return rows, nil
}
func (v *configurationOverlay) ValidateModelIdentity(id domain.ID, model domain.Model) error {
	rows, err := all(v, domain.ModelKind)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ID == id {
			continue
		}
		other, err := store.Decode[domain.Model](row)
		if err != nil {
			return err
		}
		if other.SameIdentity(model) || (other.Alias != "" && other.Alias == model.NativeID) || (model.Alias != "" && (model.Alias == other.Alias || model.Alias == other.NativeID || model.Alias == string(row.ID))) {
			return transferConflict()
		}
	}
	return nil
}

func (v *configurationOverlay) ProviderPresetExists(preset domain.ProviderPresetID, except domain.ID) (bool, error) {
	found, err := v.tx.ProviderPresetExists(preset, except)
	if err != nil || found {
		return found, err
	}
	for id, record := range v.staged {
		if id == except || record.Kind != domain.ProviderKind {
			continue
		}
		provider, err := store.Decode[domain.Provider](record)
		if err != nil {
			return false, err
		}
		if provider.PresetID != nil && *provider.PresetID == preset {
			return true, nil
		}
	}
	return false, nil
}

func buildConfigurationPlan(tx *store.Tx, selection domain.ConfigurationImportSelection) (domain.ConfigurationImportPlan, error) {
	plan := domain.ConfigurationImportPlan{Version: domain.ConfigurationBundleVersion, Changes: []domain.ConfigurationChange{}, Machines: []domain.ConfigurationTargetMachine{}}
	bundle := selection.Bundle
	raw, err := json.Marshal(bundle)
	if err != nil {
		return plan, err
	}
	if (bundle.Version != domain.ConfigurationBundleVersion) || len(bundle.Entries) == 0 {
		return plan, transferInvalid()
	}
	if len(bundle.Entries) > domain.MaxConfigurationEntries || len(raw) > domain.MaxConfigurationBundleBytes || len(bundle.Machines) > domain.MaxConfigurationCheckouts || len(selection.Bindings) > len(bundle.Entries) || len(selection.Machines) > len(bundle.Machines) || len(selection.Checkouts) > domain.MaxConfigurationCheckouts {
		return plan, transferLimit()
	}
	source := map[domain.ID]domain.ConfigurationEntry{}
	targets := map[domain.ID]domain.ID{}
	bindings := map[domain.ID]domain.ConfigurationBinding{}
	for _, entry := range bundle.Entries {
		if entry.ID.Validate() != nil || source[entry.ID].ID != "" || !slices.Contains(portableKinds, entry.Kind) {
			return plan, transferInvalid()
		}
		if _, err := portableValue(entry.Kind, entry.Document, true); err != nil {
			return plan, err
		}
		source[entry.ID] = entry
		targets[entry.ID] = domain.NewID()
	}
	usedTargets := map[domain.ID]bool{}
	for _, binding := range selection.Bindings {
		entry, ok := source[binding.SourceID]
		if !ok || bindings[binding.SourceID].SourceID != "" || binding.TargetID.Validate() != nil || usedTargets[binding.TargetID] || binding.ExpectedRevision == 0 || entry.Kind == domain.AccountKind || (binding.Action != domain.ConfigurationReuse && !(binding.Action == domain.ConfigurationReplace && entry.Kind == domain.SettingsKind)) {
			return plan, transferInvalid()
		}
		bindings[binding.SourceID] = binding
		targets[binding.SourceID] = binding.TargetID
		usedTargets[binding.TargetID] = true
	}
	machineSources := map[domain.ID]domain.ConfigurationMachine{}
	for _, machine := range bundle.Machines {
		if machine.ID.Validate() != nil || machineSources[machine.ID].ID != "" || source[machine.ID].ID != "" || domain.Text(machine.Name, "machine name", 256, true) != nil || !slices.Contains([]string{"darwin", "windows", "linux"}, machine.OS) || !slices.Contains([]string{"arm64", "amd64"}, machine.Architecture) {
			return plan, transferInvalid()
		}
		machineSources[machine.ID] = machine
	}
	machines := map[domain.ID]domain.ID{}
	targetMachines := map[domain.ID]bool{}
	for _, binding := range selection.Machines {
		if machineSources[binding.SourceID].ID == "" || machines[binding.SourceID] != "" || binding.TargetID.Validate() != nil || targetMachines[binding.TargetID] {
			return plan, transferInvalid()
		}
		record, err := tx.Get(domain.MachineKind, binding.TargetID)
		if err != nil {
			return plan, err
		}
		machine, err := store.Decode[domain.Machine](record)
		if err != nil {
			return plan, err
		}
		if machine.Disabled {
			return plan, transferConflict()
		}
		machines[binding.SourceID] = binding.TargetID
		targetMachines[binding.TargetID] = true
		plan.Machines = append(plan.Machines, domain.ConfigurationTargetMachine{ID: record.ID, Name: machine.Name, OS: machine.OS, Architecture: machine.Architecture})
	}
	if len(machines) != len(machineSources) {
		return plan, transferInvalid()
	}
	type checkoutKey struct{ repository, machine domain.ID }
	checkouts := map[checkoutKey]string{}
	for _, binding := range selection.Checkouts {
		key := checkoutKey{binding.RepositoryID, binding.MachineID}
		if source[key.repository].Kind != domain.RepositoryKind || machines[key.machine] == "" || checkouts[key] != "" || domain.Text(binding.Path, "checkout path", 4096, true) != nil {
			return plan, transferInvalid()
		}
		checkouts[key] = binding.Path
	}
	usedMachines := map[domain.ID]bool{}
	ref := func(id domain.ID, kind domain.Kind) (domain.ID, error) {
		if source[id].Kind != kind {
			return "", transferInvalid()
		}
		return targets[id], nil
	}
	rewrite := func(id *domain.ID, kind domain.Kind) error {
		if *id == "" {
			return nil
		}
		value, err := ref(*id, kind)
		*id = value
		return err
	}
	rewriteIDs := func(ids []domain.ID, kind domain.Kind) error {
		for i := range ids {
			if err := rewrite(&ids[i], kind); err != nil {
				return err
			}
		}
		return nil
	}
	rewriteRemediation := func(policy *domain.RemediationPolicy) error {
		if policy == nil {
			return nil
		}
		if err := rewrite(&policy.AgentID, domain.AgentKind); err != nil {
			return err
		}
		if policy.MachineID != "" {
			id := policy.MachineID
			if machines[id] == "" {
				return transferInvalid()
			}
			usedMachines[id] = true
			policy.MachineID = machines[id]
		}
		return nil
	}
	for _, entry := range bundle.Entries {
		value, err := portableValue(entry.Kind, entry.Document, true)
		if err != nil {
			return plan, err
		}
		switch v := value.(type) {
		case *domain.Model:
			if v.SourceKind != domain.SubscriptionModel {
				err = rewrite(&v.ProviderID, domain.ProviderKind)
			}
		case *domain.Account:
			if v.Type != domain.SubscriptionAccount {
				err = rewrite(&v.ProviderID, domain.ProviderKind)
			}
		case *domain.Agent:
			if err = rewrite(&v.ModelID, domain.ModelKind); err == nil {
				err = rewriteIDs(v.Templates, domain.TemplateKind)
			}
			if err == nil {
				for i := range v.Accounts {
					if err = rewrite(&v.Accounts[i].ID, domain.AccountKind); err != nil {
						break
					}
				}
			}
		case *domain.Project:
			if err = rewriteIDs(v.Repositories, domain.RepositoryKind); err == nil {
				err = rewrite(&v.PrimaryRepository, domain.RepositoryKind)
			}
			if err == nil {
				err = rewriteIDs(v.Agents.IDs, domain.AgentKind)
			}
			if err == nil {
				err = rewriteIDs(v.Accounts.IDs, domain.AccountKind)
			}
		case *domain.Repository:
			for i, c := range v.Checkouts {
				key := checkoutKey{entry.ID, c.MachineID}
				path, ok := checkouts[key]
				if !ok || machines[c.MachineID] == "" {
					return plan, transferInvalid()
				}
				usedMachines[c.MachineID] = true
				v.Checkouts[i] = domain.Checkout{MachineID: machines[c.MachineID], Path: path}
				delete(checkouts, key)
			}
			err = rewriteRemediation(v.Remediation)
		case *domain.Settings:
			err = rewriteRemediation(&v.Remediation)
		}
		if err != nil {
			return plan, err
		}
		if err = value.Validate(); err != nil {
			return plan, err
		}
		after, err := json.Marshal(value)
		if err != nil {
			return plan, err
		}
		change := domain.ConfigurationChange{SourceID: entry.ID, ID: targets[entry.ID], Kind: entry.Kind, Action: domain.ConfigurationCreate, After: after}
		if binding, ok := bindings[entry.ID]; ok {
			old, err := tx.Get(entry.Kind, binding.TargetID)
			if err != nil {
				return plan, err
			}
			if old.Revision != binding.ExpectedRevision {
				return plan, transferConflict()
			}
			change.Action, change.ExpectedRevision = binding.Action, binding.ExpectedRevision
			change.Before, err = portableDocument(old.Kind, old.Data)
			if err != nil {
				return plan, err
			}
			if change.Action == domain.ConfigurationReuse && !bytes.Equal(change.Before, change.After) {
				return plan, domain.Fail(domain.Conflict, "Explicitly reused configuration does not match the imported values.", "Preserve both configurations as separate entries, or edit the import before requesting another preview.")
			}
		}
		plan.Changes = append(plan.Changes, change)
	}
	if len(checkouts) != 0 || len(usedMachines) != len(machineSources) {
		return plan, transferInvalid()
	}
	sort.Slice(plan.Changes, func(i, j int) bool {
		a, b := plan.Changes[i], plan.Changes[j]
		ka, kb := slices.Index(portableKinds, a.Kind), slices.Index(portableKinds, b.Kind)
		if ka != kb {
			return ka < kb
		}
		return a.SourceID < b.SourceID
	})
	slices.SortFunc(plan.Machines, func(a, b domain.ConfigurationTargetMachine) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return plan, validateConfigurationPlan(tx, plan)
}

func validateConfigurationPlan(tx *store.Tx, plan domain.ConfigurationImportPlan) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if plan.Version != domain.ConfigurationBundleVersion || len(plan.Changes) == 0 || len(plan.Changes) > domain.MaxConfigurationEntries || len(raw) > domain.MaxConfigurationPlanBytes || len(plan.Machines) > domain.MaxConfigurationCheckouts {
		return transferLimit()
	}
	current, err := configurationSnapshot(tx)
	if err != nil {
		return err
	}
	overlay := &configurationOverlay{tx: tx, current: current, staged: map[domain.ID]store.Record{}}
	machines := map[domain.ID]bool{}
	for _, target := range plan.Machines {
		if target.ID.Validate() != nil || machines[target.ID] {
			return transferInvalid()
		}
		row, err := tx.Get(domain.MachineKind, target.ID)
		if err != nil {
			return err
		}
		machine, err := store.Decode[domain.Machine](row)
		if err != nil {
			return err
		}
		if machine.Disabled || machine.OS != target.OS || machine.Architecture != target.Architecture || machine.Name != target.Name {
			return transferConflict()
		}
		machines[target.ID] = true
	}
	sources := map[domain.ID]bool{}
	checkouts := 0
	for _, change := range plan.Changes {
		if change.ID.Validate() != nil || change.SourceID.Validate() != nil || sources[change.SourceID] || overlay.staged[change.ID].ID != "" || !slices.Contains(portableKinds, change.Kind) {
			return transferInvalid()
		}
		sources[change.SourceID] = true
		old, exists := current[change.ID]
		switch change.Action {
		case domain.ConfigurationCreate:
			if err := tx.RequireUnusedID(change.ID); err != nil {
				return err
			}
			if exists || change.ExpectedRevision != 0 || len(change.Before) != 0 {
				return transferConflict()
			}
		case domain.ConfigurationReuse, domain.ConfigurationReplace:
			if !exists || old.Kind != change.Kind || old.Revision != change.ExpectedRevision || change.Kind == domain.AccountKind || (change.Action == domain.ConfigurationReplace && change.Kind != domain.SettingsKind) {
				return transferConflict()
			}
			before, err := portableDocument(old.Kind, old.Data)
			if err != nil {
				return err
			}
			if !bytes.Equal(before, change.Before) || (change.Action == domain.ConfigurationReuse && !bytes.Equal(before, change.After)) {
				return transferConflict()
			}
		default:
			return transferInvalid()
		}
		value, err := portableValue(change.Kind, change.After, true)
		if err != nil {
			return err
		}
		switch v := value.(type) {
		case *domain.Repository:
			if v.Remediation != nil && v.Remediation.MachineID != "" && !machines[v.Remediation.MachineID] {
				return transferInvalid()
			}
			for _, c := range v.Checkouts {
				if !machines[c.MachineID] {
					return transferInvalid()
				}
				checkouts++
			}
		case *domain.Settings:
			if v.Remediation.MachineID != "" && !machines[v.Remediation.MachineID] {
				return transferInvalid()
			}
		}
		if checkouts > domain.MaxConfigurationCheckouts {
			return transferLimit()
		}
		if change.Action == domain.ConfigurationReuse {
			overlay.staged[change.ID] = old
		} else {
			overlay.staged[change.ID] = store.Record{ID: change.ID, Kind: change.Kind, Data: change.After}
		}
	}
	settings, err := all(overlay, domain.SettingsKind)
	if err != nil {
		return err
	}
	if len(settings) > 1 {
		return transferConflict()
	}
	for _, change := range plan.Changes {
		if change.Action == domain.ConfigurationReuse {
			continue
		}
		value, err := portableValue(change.Kind, change.After, true)
		if err != nil {
			return err
		}
		overlay.self = change.ID
		if err = validateRelationships(overlay, change.Kind, change.ID, change.ExpectedRevision, value); err != nil {
			return err
		}
	}
	return nil
}
func configurationPlanScope(actor domain.Principal, plan domain.ConfigurationImportPlan) string {
	raw, _ := json.Marshal(struct {
		Actor domain.Principal
		Plan  domain.ConfigurationImportPlan
	}{actor, plan})
	digest := sha256.Sum256(raw)
	return "configuration-import:" + hex.EncodeToString(digest[:])
}
func (s *Service) ExportConfiguration(ctx context.Context, req *connect.Request[pb.ExportConfigurationRequest]) (*connect.Response[pb.ExportConfigurationResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := configurationActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var bundle domain.ConfigurationBundle
	err := s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		bundle, err = exportConfiguration(tx)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.ExportConfigurationResponse{DocumentJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) PreviewConfigurationImport(ctx context.Context, req *connect.Request[pb.PreviewConfigurationImportRequest]) (*connect.Response[pb.PreviewConfigurationImportResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := configurationActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var selection domain.ConfigurationImportSelection
	if err = domain.Decode(req.Msg.SelectionJson, &selection); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var plan domain.ConfigurationImportPlan
	err = s.Store.Read(bounded, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		plan, err = buildConfigurationPlan(tx, selection)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	token, err := s.Identity.EncodeCursor(security.Cursor{Scope: configurationPlanScope(actor, plan)})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	raw, err := json.Marshal(domain.ConfigurationImportPreview{Plan: plan, Token: token})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.PreviewConfigurationImportResponse{PreviewJson: raw})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
