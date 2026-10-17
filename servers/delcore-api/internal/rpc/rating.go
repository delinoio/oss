// SPDX-License-Identifier: Apache-2.0
package rpc

import (
	"github.com/delinoio/oss/servers/delcore-api/internal/accounting"
	"github.com/delinoio/oss/servers/delcore-api/internal/postgres"
	"time"
)

func (c *call) ratePeriod(tenant, service, meter string, start time.Time) (accounting.Decimal, accounting.Decimal, error) {
	return postgres.RatePeriod(c.ctx, c.tx, tenant, service, meter, start)
}
