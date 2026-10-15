package runmoor

import (
	"context"
)

func hostWorkerAdmission(ctx context.Context, in HostBootstrap) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s, err := ReadSnapshot(Config{Storage: in.Storage})
	if err != nil {
		return err
	}
	r := s.Runners[in.Directory.ID]
	if r == nil || r.Forced || r.Phase != Preparing || s.HostExecutions[r.ID] == nil || s.Installation != in.Directory.Installation || s.HostDirectories[r.ID] == nil || *s.HostDirectories[r.ID] != in.Directory {
		return staleUpdate()
	}
	return nil
}
