// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"context"
	"github.com/delinoio/oss/packages/tairouter/core"
	"math"
	"net/http"
	"regexp"
	"sort"
	"time"
)

var workBuddyPackageCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func workBuddyAmount(r map[string]any) (Balance, bool) {
	total, tok := finite(r["CycleTotalCapacity"])
	used, uok := finite(r["CycleUsedCapacity"])
	frozen := 0.0
	if r["CycleFrozenCapacity"] != nil {
		var ok bool
		frozen, ok = finite(r["CycleFrozenCapacity"])
		if !ok {
			return Balance{}, false
		}
	}
	remain := math.Max(0, total-used-frozen)
	if r["CycleRemainCapacity"] != nil {
		var ok bool
		remain, ok = finite(r["CycleRemainCapacity"])
		if !ok {
			return Balance{}, false
		}
	}
	return Balance{Unit: "credits", Total: total, Used: used, Remaining: remain, Frozen: frozen}, tok && uok
}
func (a *Auth) workBuddyExpiry(ctx context.Context, s core.Scope, summary map[string]any, h http.Header) (float64, time.Time, error) {
	rows, _ := summary["Packages"].([]any)
	codes := map[string]bool{}
	for _, raw := range rows {
		r, _ := raw.(map[string]any)
		code := str(r, "PackageCode")
		if workBuddyPackageCode.MatchString(code) {
			codes[code] = true
		}
	}
	list := make([]string, 0, len(codes))
	for c := range codes {
		list = append(list, c)
	}
	sort.Strings(list)
	if len(list) == 0 {
		return 0, time.Time{}, nil
	}
	zone := time.FixedZone("UTC+8", 8*3600)
	day := a.now().In(zone).Format("2006-01-02")
	var earliest time.Time
	amount := 0.0
	for _, kind := range []string{"paid", "free"} {
		complete := false
		count := 0
		for page := 1; page <= 10; page++ {
			body := map[string]any{"PageNumber": page, "PageSize": 200, "Status": []int{0, 3}, "PackageCodes": list}
			if kind == "paid" {
				body["NeedRenewInfo"] = true
			} else {
				body["SlicePeriodStartTime"] = day + " 00:00:00"
				body["SlicePeriodEndTime"] = day + " 23:59:59"
			}
			m, e := a.workBuddy(ctx, s, "POST", "/billing/meter/get-user-resource-"+kind+"-packages", body, h)
			if e != nil {
				return 0, time.Time{}, e
			}
			rows, ok := m["Accounts"].([]any)
			if !ok || len(rows) > 200 {
				return 0, time.Time{}, ErrResponse
			}
			count += len(rows)
			for _, raw := range rows {
				r, _ := raw.(map[string]any)
				if !codes[str(r, "PackageCode")] || number(r, "CapacityType") == 4 {
					continue
				}
				n, ok := finite(r["CycleCapacityRemainPrecise"])
				if !ok || n == 0 {
					n, ok = finite(r["CycleCapacityRemain"])
				}
				if !ok || n <= 0 {
					continue
				}
				end := time.Time{}
				if v, ok := finite(r["DeductionEndTime"]); ok && v > 0 && v < 1e15 {
					end = time.UnixMilli(int64(v))
				}
				if end.IsZero() {
					for _, k := range []string{"ExpiredTime", "CycleEndTime"} {
						v := str(r, k)
						end = resetTime(v)
						if end.IsZero() {
							end, _ = time.ParseInLocation("2006-01-02 15:04:05", v, zone)
						}
						if !end.IsZero() {
							break
						}
					}
				}
				if !end.After(a.now()) {
					continue
				}
				if earliest.IsZero() || end.Before(earliest) {
					earliest = end
					amount = n
				} else if end.Equal(earliest) {
					amount += n
				}
			}
			total, tok := finite(m["TotalCount"])
			if len(rows) < 200 || (tok && float64(count) >= total) {
				complete = true
				break
			}
		}
		if !complete {
			return 0, time.Time{}, ErrResponse
		}
	}
	return amount, earliest, nil
}
