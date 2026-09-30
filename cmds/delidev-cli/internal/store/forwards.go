package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// VisitForwards pages retained metadata without buffering historical traffic.
func (t *Tx) VisitForwards(session domain.ID, visit func(Record, domain.Forward) error) error {
	var after domain.ID
	for {
		rows, err := t.List(Filter{Kind: domain.ForwardKind, SessionID: session, After: after, Limit: MaxPage})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := Decode[domain.Forward](row)
			if err != nil {
				return err
			}
			if err := visit(row, value); err != nil {
				return err
			}
			after = row.ID
		}
		if len(rows) < MaxPage {
			return nil
		}
	}
}
func (t *Tx) SessionForwardsPending(session domain.ID) (bool, error) {
	pending := false
	err := t.VisitForwards(session, func(_ Record, value domain.Forward) error { pending = pending || !value.Closed(); return nil })
	return pending, err
}
func (t *Tx) StopForwards(session, device domain.ID) error {
	return t.VisitForwards(session, func(row Record, value domain.Forward) error {
		if value.Closed() || (device != "" && value.ClientDeviceID != device && value.WorkerDeviceID != device) {
			return nil
		}
		before := value
		value.Stop()
		if before == value {
			return nil
		}
		_, err := t.Put(domain.ForwardKind, row.ID, row.Revision, row.SessionID, row.ProjectID, value)
		return err
	})
}
