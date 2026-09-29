package domain

import (
	"math/big"
	"sort"
	"strings"
	"time"
)

const UsageWindowLimit = 366 * 24 * time.Hour
const UsageDayBucketLimit = 370
const UsageModelGroupLimit = 500
const UsageRankedModelLimit = 5

type UsageTimeGranularity int32

const (
	UsageTimeGranularityUnspecified UsageTimeGranularity = 0
	UsageTimeGranularityDay         UsageTimeGranularity = 1
)

type UsageSelection struct {
	From        time.Time
	Until       time.Time
	SessionID   ID
	ProjectID   ID
	AccountID   ID
	ProviderID  ID
	ModelID     ID
	GeneralChat bool
	Granularity UsageTimeGranularity
	TimeZone    string
}

func (f UsageSelection) Validate() error {
	if f.From.UnixMilli() <= 0 || f.Until.UnixMilli() > 253402300799999 || !f.Until.After(f.From) || f.Until.Sub(f.From) > UsageWindowLimit || (f.GeneralChat && f.ProjectID != "") {
		return Fail(InvalidArgument, "Invalid usage time range or project selection.", "Select a positive half-open time range of at most 366 days, and either a project or General Chat.")
	}
	for _, id := range []ID{f.SessionID, f.ProjectID, f.AccountID, f.ProviderID, f.ModelID} {
		if id != "" && id.Validate() != nil {
			return invalidObservation()
		}
	}
	switch f.Granularity {
	case UsageTimeGranularityUnspecified:
		if f.TimeZone != "" {
			return invalidUsageTimeZone()
		}
	case UsageTimeGranularityDay:
		if _, err := usageTimeLocation(f.TimeZone); err != nil {
			return err
		}
	default:
		return Fail(InvalidArgument, "Invalid usage time granularity.", "Choose a supported usage granularity.")
	}
	return nil
}

func usageTimeLocation(value string) (*time.Location, error) {
	if Text(value, "IANA timezone", 256, true) != nil || value == "Local" || strings.TrimSpace(value) != value {
		return nil, invalidUsageTimeZone()
	}
	zone, err := time.LoadLocation(value)
	if err != nil {
		return nil, invalidUsageTimeZone()
	}
	return zone, nil
}

func invalidUsageTimeZone() error {
	return Fail(InvalidArgument, "Invalid usage timezone.", "Choose an explicit IANA timezone from the bundled timezone database.")
}

// UsageDayBuckets partitions the whole selected interval at local calendar
// midnights. It intentionally derives every next boundary from the timezone
// database instead of adding a fixed 24-hour duration, preserving DST days.
func (f UsageSelection) UsageDayBuckets() ([]UsageAnalyticsDay, error) {
	if f.Granularity != UsageTimeGranularityDay {
		return nil, nil
	}
	zone, err := usageTimeLocation(f.TimeZone)
	if err != nil {
		return nil, err
	}
	cursor := f.From
	buckets := make([]UsageAnalyticsDay, 0, 32)
	for cursor.Before(f.Until) {
		if len(buckets) >= UsageDayBucketLimit {
			return nil, Fail(InvalidArgument, "The usage range has too many calendar days.", "Choose a range that contains at most 370 local calendar days.")
		}
		local := cursor.In(zone)
		year, month, day := local.Date()
		candidate := time.Date(year, month, day, 0, 0, 0, 0, zone)
		if candidate.After(cursor) {
			return nil, Fail(InvalidArgument, "The usage timezone produced an invalid calendar boundary.", "Choose another explicit IANA timezone or a shorter range.")
		}
		var next time.Time
		for step := 1; step <= 4; step++ {
			civil := time.Date(year, month, day+step, 0, 0, 0, 0, time.UTC)
			boundary := time.Date(civil.Year(), civil.Month(), civil.Day(), 0, 0, 0, 0, zone)
			if boundary.After(cursor) {
				next = boundary
				break
			}
		}
		if next.IsZero() {
			return nil, Fail(InvalidArgument, "The usage timezone has an unsupported calendar transition.", "Choose another explicit IANA timezone.")
		}
		until := next
		if until.After(f.Until) {
			until = f.Until
		}
		buckets = append(buckets, UsageAnalyticsDay{From: cursor, Until: until})
		cursor = until
	}
	return buckets, nil
}

// Empty KnownTotal means no measurement, while "0" is a measured zero. Decimal
// strings preserve exact sums beyond JavaScript and signed-64-bit integer limits.
type UsageMeasure struct {
	KnownTotal           string `json:"known_total"`
	MeasuredResponses    uint32 `json:"measured_responses"`
	UnavailableResponses uint32 `json:"unavailable_responses"`
}

func (m *UsageMeasure) add(value *int64) {
	if value == nil {
		m.UnavailableResponses++
		return
	}
	var sum big.Int
	if m.KnownTotal != "" {
		sum.SetString(m.KnownTotal, 10)
	}
	sum.Add(&sum, big.NewInt(*value))
	m.KnownTotal = sum.String()
	m.MeasuredResponses++
}

type UsageTotals struct {
	Responses       uint32       `json:"responses"`
	Input           UsageMeasure `json:"input"`
	CachedInput     UsageMeasure `json:"cached_input"`
	CacheWriteInput UsageMeasure `json:"cache_write_input"`
	Output          UsageMeasure `json:"output"`
	ReasoningOutput UsageMeasure `json:"reasoning_output"`
	Total           UsageMeasure `json:"total"`
}

func mergeUsageMeasure(target *UsageMeasure, source UsageMeasure) {
	if source.KnownTotal != "" {
		var sum big.Int
		if target.KnownTotal != "" {
			sum.SetString(target.KnownTotal, 10)
		}
		var value big.Int
		value.SetString(source.KnownTotal, 10)
		sum.Add(&sum, &value)
		target.KnownTotal = sum.String()
	}
	target.MeasuredResponses += source.MeasuredResponses
	target.UnavailableResponses += source.UnavailableResponses
}

func (t *UsageTotals) Merge(source UsageTotals) {
	t.Responses += source.Responses
	mergeUsageMeasure(&t.Input, source.Input)
	mergeUsageMeasure(&t.CachedInput, source.CachedInput)
	mergeUsageMeasure(&t.CacheWriteInput, source.CacheWriteInput)
	mergeUsageMeasure(&t.Output, source.Output)
	mergeUsageMeasure(&t.ReasoningOutput, source.ReasoningOutput)
	mergeUsageMeasure(&t.Total, source.Total)
}

func (t *UsageTotals) Add(counts *NativeTokenCounts) {
	t.Responses++
	if counts == nil {
		counts = &NativeTokenCounts{}
	}
	t.Input.add(counts.Input)
	t.CachedInput.add(counts.Cached)
	t.CacheWriteInput.add(counts.CacheWrite)
	t.Output.add(counts.Output)
	t.ReasoningOutput.add(counts.Reasoning)
	t.Total.add(counts.Total)
}

type UsageGroup struct {
	SessionID  ID             `json:"session_id"`
	ProjectID  ID             `json:"project_id,omitempty"`
	AccountID  ID             `json:"account_id"`
	ProviderID ID             `json:"provider_id"`
	ModelID    ID             `json:"model_id"`
	Totals     UsageTotals    `json:"totals"`
	Estimates  EstimateTotals `json:"estimates"`
}

type UsageAnalyticsDay struct {
	From   time.Time   `json:"from"`
	Until  time.Time   `json:"until"`
	Totals UsageTotals `json:"totals"`
}

type UsageAnalyticsModel struct {
	ProviderID ID          `json:"provider_id"`
	ModelID    ID          `json:"model_id"`
	Provider   string      `json:"provider_name,omitempty"`
	Model      string      `json:"model_name,omitempty"`
	Totals     UsageTotals `json:"totals"`
}

type UsageOtherModels struct {
	ModelCount uint32      `json:"model_count"`
	Totals     UsageTotals `json:"totals"`
}

type UsageAnalytics struct {
	Granularity UsageTimeGranularity  `json:"granularity"`
	TimeZone    string                `json:"time_zone"`
	Days        []UsageAnalyticsDay   `json:"days"`
	Models      []UsageAnalyticsModel `json:"models"`
	OtherModels *UsageOtherModels     `json:"other_models,omitempty"`
}

type UsageSummary struct {
	Estimates                         EstimateTotals  `json:"estimates"`
	Pricing                           []PricingUsage  `json:"pricing"`
	Totals                            UsageTotals     `json:"totals"`
	Groups                            []UsageGroup    `json:"groups"`
	Analytics                         *UsageAnalytics `json:"analytics,omitempty"`
	AcceptedExecutionsWithoutResponse uint32          `json:"accepted_executions_without_response"`
}

func SortUsageAnalyticsModels(models []UsageAnalyticsModel) {
	sort.Slice(models, func(i, j int) bool {
		left, right := models[i], models[j]
		leftKnown := left.Totals.Total.MeasuredResponses > 0 && left.Totals.Total.KnownTotal != ""
		rightKnown := right.Totals.Total.MeasuredResponses > 0 && right.Totals.Total.KnownTotal != ""
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown {
			var a, b big.Int
			a.SetString(left.Totals.Total.KnownTotal, 10)
			b.SetString(right.Totals.Total.KnownTotal, 10)
			if comparison := a.Cmp(&b); comparison != 0 {
				return comparison > 0
			}
		}
		if left.ProviderID != right.ProviderID {
			return left.ProviderID < right.ProviderID
		}
		return left.ModelID < right.ModelID
	})
}
