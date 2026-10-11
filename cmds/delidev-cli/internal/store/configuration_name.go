package store

import (
	"encoding/json"
	"log/slog"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type configurationNameView interface {
	List(Filter) ([]Record, error)
}

// ValidateConfigurationName also accepts a complete import overlay. The final
// publication calls this within the serialized mutation transaction, so a prior
// admission or native inspection cannot reserve an otherwise available name.
func ValidateConfigurationName(view configurationNameView, kind domain.Kind, id domain.ID, body []byte) error {
	if kind != domain.ProjectKind && kind != domain.RepositoryKind {
		return nil
	}
	var candidate struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &candidate) != nil || domain.Text(candidate.Name, "name", 256, true) != nil {
		return domain.Fail(domain.InvalidArgument, "Invalid configuration name.", "Enter a valid bounded name.")
	}
	key := domain.ConfigurationNameKey(candidate.Name)
	filter := Filter{Kind: kind, Limit: MaxPage}
	for {
		rows, err := view.List(filter)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ID == id {
				continue
			}
			var other struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(row.Data, &other) != nil {
				return corrupt()
			}
			if domain.ConfigurationNameKey(other.Name) == key {
				slog.Info("configuration name publication rejected", "kind", kind, "cause", domain.ConfigurationNameConflictCause)
				return domain.ConfigurationNameConflict(kind)
			}
		}
		if len(rows) < filter.Limit {
			return nil
		}
		filter.After = rows[len(rows)-1].ID
	}
}
