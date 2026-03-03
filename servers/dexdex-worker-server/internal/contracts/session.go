package contracts

import v1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"

type SessionRunResult struct {
	Outputs []*v1.SessionOutputEvent
	Status  v1.AgentSessionStatus
}
