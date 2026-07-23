// Package catalog loads and validates delibase's checked-in catalog contract.
package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
)

const (
	currentVersion  = 1
	maxDocumentSize = 1 << 20
)

var (
	slugPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)
	meterKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

// Specification is the complete desired catalog state synchronized at startup.
type Specification struct {
	Version     int          `json:"version"`
	Apps        []App        `json:"apps"`
	Meters      []Meter      `json:"meters"`
	Prices      []Price      `json:"prices"`
	Services    []Service    `json:"services"`
	PolarMeters []PolarMeter `json:"polar_meters"`
}

type App struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	Enabled     *bool  `json:"enabled"`
}

type Meter struct {
	ID                    string `json:"id"`
	AppID                 string `json:"app_id"`
	Key                   string `json:"key"`
	Name                  string `json:"name"`
	Description           string `json:"description"`
	UnitName              string `json:"unit_name"`
	UnitPrecision         *int   `json:"unit_precision"`
	ReservationTTLSeconds int64  `json:"reservation_ttl_seconds"`
	Enabled               *bool  `json:"enabled"`
}

type Price struct {
	ID               string     `json:"id"`
	MeterID          string     `json:"meter_id"`
	USDMicrosPerUnit int64      `json:"usd_micros_per_unit"`
	EffectiveFrom    time.Time  `json:"effective_from"`
	EffectiveUntil   *time.Time `json:"effective_until"`
}

type Service struct {
	ID              string   `json:"id"`
	LogtoClientID   string   `json:"logto_client_id"`
	Name            string   `json:"name"`
	Enabled         *bool    `json:"enabled"`
	AllowedMeterIDs []string `json:"allowed_meter_ids"`
}

type PolarMeter struct {
	MeterID      string `json:"meter_id"`
	PolarMeterID string `json:"polar_meter_id"`
}

// Load reads a bounded JSON document and returns only diagnostic-safe errors.
func Load(path string) (Specification, error) {
	file, err := os.Open(path)
	if err != nil {
		return Specification{}, errors.New("catalog: file is unavailable")
	}
	defer file.Close()

	document, err := io.ReadAll(io.LimitReader(file, maxDocumentSize+1))
	if err != nil {
		return Specification{}, errors.New("catalog: file could not be read")
	}
	if len(document) > maxDocumentSize {
		return Specification{}, errors.New("catalog: document exceeds size limit")
	}
	return parse(document)
}

func parse(document []byte) (Specification, error) {
	var specification Specification
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&specification); err != nil {
		return Specification{}, errors.New("catalog: invalid JSON document")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Specification{}, errors.New("catalog: invalid JSON document")
	}
	if err := specification.validate(); err != nil {
		return Specification{}, err
	}
	return specification, nil
}

func (specification Specification) validate() error {
	if specification.Version != currentVersion {
		return errors.New("catalog: unsupported version")
	}
	if specification.Apps == nil || specification.Meters == nil ||
		specification.Prices == nil || specification.Services == nil ||
		specification.PolarMeters == nil {
		return errors.New("catalog: all collections are required")
	}

	apps := make(map[string]App, len(specification.Apps))
	appSlugs := make(map[string]struct{}, len(specification.Apps))
	for _, app := range specification.Apps {
		if !validID(app.ID) || !slugPattern.MatchString(app.Slug) ||
			len(app.Name) < 1 || len(app.Name) > 120 || app.Enabled == nil {
			return errors.New("catalog: invalid app")
		}
		if _, duplicate := apps[app.ID]; duplicate {
			return errors.New("catalog: duplicate app")
		}
		if _, duplicate := appSlugs[app.Slug]; duplicate {
			return errors.New("catalog: duplicate app slug")
		}
		apps[app.ID] = app
		appSlugs[app.Slug] = struct{}{}
	}

	meters := make(map[string]Meter, len(specification.Meters))
	meterKeys := make(map[string]struct{}, len(specification.Meters))
	for _, meter := range specification.Meters {
		app, appExists := apps[meter.AppID]
		key := meter.AppID + "\x00" + meter.Key
		if !validID(meter.ID) || !appExists || !meterKeyPattern.MatchString(meter.Key) ||
			len(meter.Name) < 1 || len(meter.Name) > 120 ||
			len(meter.UnitName) < 1 || len(meter.UnitName) > 64 ||
			meter.UnitPrecision == nil || *meter.UnitPrecision != 0 ||
			meter.ReservationTTLSeconds < 1 || meter.ReservationTTLSeconds > 86400 ||
			meter.Enabled == nil || (*meter.Enabled && !*app.Enabled) {
			return errors.New("catalog: invalid meter")
		}
		if _, duplicate := meters[meter.ID]; duplicate {
			return errors.New("catalog: duplicate meter")
		}
		if _, duplicate := meterKeys[key]; duplicate {
			return errors.New("catalog: duplicate meter key")
		}
		meters[meter.ID] = meter
		meterKeys[key] = struct{}{}
	}

	prices := make(map[string]struct{}, len(specification.Prices))
	pricesByMeter := make(map[string][]Price, len(meters))
	for _, price := range specification.Prices {
		if !validID(price.ID) || price.USDMicrosPerUnit < 0 ||
			price.EffectiveFrom.IsZero() {
			return errors.New("catalog: invalid price")
		}
		if _, exists := meters[price.MeterID]; !exists {
			return errors.New("catalog: price references an unknown meter")
		}
		if price.EffectiveUntil != nil &&
			!price.EffectiveUntil.After(price.EffectiveFrom) {
			return errors.New("catalog: invalid price window")
		}
		if _, duplicate := prices[price.ID]; duplicate {
			return errors.New("catalog: duplicate price")
		}
		prices[price.ID] = struct{}{}
		pricesByMeter[price.MeterID] = append(pricesByMeter[price.MeterID], price)
	}
	for meterID := range meters {
		meterPrices := pricesByMeter[meterID]
		if len(meterPrices) == 0 {
			return errors.New("catalog: meter is missing a price")
		}
		sort.Slice(meterPrices, func(left, right int) bool {
			return meterPrices[left].EffectiveFrom.Before(meterPrices[right].EffectiveFrom)
		})
		for index := 1; index < len(meterPrices); index++ {
			previousEnd := meterPrices[index-1].EffectiveUntil
			if previousEnd == nil || meterPrices[index].EffectiveFrom.Before(*previousEnd) {
				return errors.New("catalog: overlapping price windows")
			}
		}
	}

	serviceIDs := make(map[string]struct{}, len(specification.Services))
	serviceClients := make(map[string]struct{}, len(specification.Services))
	meterServiceMappings := make(map[string]struct{}, len(meters))
	for _, service := range specification.Services {
		if !validID(service.ID) || len(service.LogtoClientID) < 1 ||
			len(service.LogtoClientID) > 255 || len(service.Name) < 1 ||
			len(service.Name) > 120 || service.Enabled == nil ||
			service.AllowedMeterIDs == nil {
			return errors.New("catalog: invalid service")
		}
		if _, duplicate := serviceIDs[service.ID]; duplicate {
			return errors.New("catalog: duplicate service")
		}
		if _, duplicate := serviceClients[service.LogtoClientID]; duplicate {
			return errors.New("catalog: duplicate service client")
		}
		serviceIDs[service.ID] = struct{}{}
		serviceClients[service.LogtoClientID] = struct{}{}
		allowed := make(map[string]struct{}, len(service.AllowedMeterIDs))
		for _, meterID := range service.AllowedMeterIDs {
			if _, exists := meters[meterID]; !exists {
				return errors.New("catalog: service references an unknown meter")
			}
			if _, duplicate := allowed[meterID]; duplicate {
				return errors.New("catalog: duplicate service meter mapping")
			}
			allowed[meterID] = struct{}{}
			if *service.Enabled {
				meterServiceMappings[meterID] = struct{}{}
			}
		}
	}

	polarMappings := make(map[string]struct{}, len(specification.PolarMeters))
	polarIDs := make(map[string]struct{}, len(specification.PolarMeters))
	for _, mapping := range specification.PolarMeters {
		if _, exists := meters[mapping.MeterID]; !exists ||
			len(mapping.PolarMeterID) < 1 || len(mapping.PolarMeterID) > 255 {
			return errors.New("catalog: invalid Polar meter mapping")
		}
		if _, duplicate := polarMappings[mapping.MeterID]; duplicate {
			return errors.New("catalog: duplicate Polar meter mapping")
		}
		if _, duplicate := polarIDs[mapping.PolarMeterID]; duplicate {
			return errors.New("catalog: duplicate Polar meter identifier")
		}
		polarMappings[mapping.MeterID] = struct{}{}
		polarIDs[mapping.PolarMeterID] = struct{}{}
	}
	for meterID, meter := range meters {
		if _, exists := polarMappings[meterID]; !exists {
			return errors.New("catalog: meter is missing a Polar mapping")
		}
		if *meter.Enabled {
			if _, exists := meterServiceMappings[meterID]; !exists {
				return errors.New("catalog: enabled meter is missing a service mapping")
			}
		}
	}
	return nil
}

func validID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil &&
		parsed.Version() == 7 && parsed.String() == value
}
