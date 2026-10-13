package server

import (
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

// requireExecutionJobBudget is the admission-time half of the execution Job
// size contract. It runs before any workspace job exists, so a rejected request
// never starts clone or preparation ownership and never publishes a partial
// session. checkedExecutionAssignment rechecks the actual bytes before dispatch.
func requireExecutionJobBudget(tx *store.Tx, session domain.Session, preparation workspace.PrepareRequest, input domain.SessionInput) error {
	budget, err := executionJobBudget(tx, session, preparation, input)
	if err != nil {
		return err
	}
	return budget.Require(nil)
}

func executionJobBudget(tx *store.Tx, session domain.Session, preparation workspace.PrepareRequest, input domain.SessionInput) (domain.ExecutionJobBudget, error) {
	var budget domain.ExecutionJobBudget
	raw, err := json.Marshal(preparation)
	if err != nil {
		return budget, err
	}
	budget.PreparationBytes = len(raw)
	if budget.ManifestBytes, err = preparation.MaxManifestBytes(); err != nil {
		return budget, err
	}
	if raw, err = json.Marshal(input); err != nil {
		return budget, err
	}
	budget.InputBytes = len(raw)
	for _, repository := range preparation.Repositories {
		if repository.PRTarget != nil {
			// A manual PR fix embeds its Git target again as the remediation.
			if raw, err = json.Marshal(repository.PRTarget); err != nil {
				return budget, err
			}
			budget.ExtraBytes += len(raw)
		}
	}
	budget.ConfigurationBytes = executionConfigurationBytes(tx, session.AgentID)
	return budget, nil
}

// executionConfigurationBytes bounds the variable part of the frozen
// execution configuration from the selected Agent Worker: the ordered applied
// templates (stored once as templates and once as joined instructions) and the
// account list. Unreadable references contribute nothing here because the
// first-execution claim rejects them with its own precise error.
func executionConfigurationBytes(tx *store.Tx, agentID domain.ID) int {
	record, err := tx.Get(domain.AgentKind, agentID)
	if err != nil {
		return 0
	}
	agent, err := store.Decode[domain.Agent](record)
	if err != nil {
		return 0
	}
	templates := make([]domain.AppliedTemplate, 0, len(agent.Templates))
	parts := make([]string, 0, len(agent.Templates))
	for _, id := range agent.Templates {
		row, err := tx.Get(domain.TemplateKind, id)
		if err != nil {
			continue
		}
		template, err := store.Decode[domain.Template](row)
		if err != nil {
			continue
		}
		templates = append(templates, domain.AppliedTemplate{ID: id, Revision: row.Revision, Contents: template.Contents})
		parts = append(parts, template.Contents)
	}
	accounts := agent.Accounts
	for _, route := range agent.Routes {
		if len(route.Accounts) > len(accounts) {
			accounts = route.Accounts
		}
	}
	configuration := domain.ExecutionConfiguration{Accounts: accounts, Templates: templates, Instructions: strings.Join(parts, "\n\n"), Options: agent.Options}
	raw, err := json.Marshal(configuration)
	if err != nil {
		return 0
	}
	return len(raw)
}
