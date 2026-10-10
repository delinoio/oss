// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type currentTimeDelivery string

const (
	currentTimeSent      currentTimeDelivery = "sent"
	currentTimeRejected  currentTimeDelivery = "rejected"
	currentTimeUncertain currentTimeDelivery = "uncertain"
)

func configureCurrentTime(config *Config) (bool, error) {
	if !config.EnableCurrentTime {
		return false, nil
	}
	if config.Mode != ThreadProtocol || config.Sidechat != "" || config.ModelObservation || config.API != nil && config.API.TitleProfile {
		return false, incompatible()
	}
	config.Process.Args = append(config.Process.Args,
		"-c", "features.current_time_reminder.enabled=true",
		"-c", `features.current_time_reminder.clock_source="external"`,
	)
	return true, nil
}

func currentTimeConfigEnabled(config map[string]json.RawMessage) bool {
	var features map[string]json.RawMessage
	if domain.Decode(config["features"], &features) != nil || features == nil {
		return false
	}
	var reminder struct {
		Enabled     *bool  `json:"enabled"`
		ClockSource string `json:"clock_source"`
	}
	if json.Unmarshal(features["current_time_reminder"], &reminder) != nil {
		return false
	}
	return reminder.Enabled != nil && *reminder.Enabled && reminder.ClockSource == "external"
}

func currentTimeFeatureEnabled(raw json.RawMessage) bool {
	var observed struct {
		Data []struct {
			Name           string  `json:"name"`
			Stage          string  `json:"stage"`
			DisplayName    *string `json:"displayName"`
			Description    *string `json:"description"`
			Announcement   *string `json:"announcement"`
			Enabled        *bool   `json:"enabled"`
			DefaultEnabled *bool   `json:"defaultEnabled"`
		} `json:"data"`
		NextCursor *string `json:"nextCursor"`
	}
	if domain.Decode(raw, &observed) != nil || observed.Data == nil || len(observed.Data) > 256 || observed.NextCursor != nil {
		return false
	}
	found := false
	seen := make(map[string]bool, len(observed.Data))
	for _, feature := range observed.Data {
		if domain.Text(feature.Name, "native feature", 128, true) != nil || seen[feature.Name] {
			return false
		}
		seen[feature.Name] = true
		if feature.Name != "current_time_reminder" {
			continue
		}
		if found || feature.Enabled == nil || !*feature.Enabled {
			return false
		}
		found = true
	}
	return found
}

func (c *Client) verifyCurrentTime(ctx context.Context, cwd string) (returned error) {
	if !c.currentTimeEnabled {
		return nil
	}
	defer c.recordFailure(ctx, domain.CodexProfile, &returned)
	if c.mode != ThreadProtocol || c.thread.Validate() != nil || cwd == "" {
		return incompatible()
	}
	response, err := c.wire.Call(ctx, domain.NewID(), "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{Cwd: cwd})
	if err != nil {
		return err
	}
	var config struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if response.ErrorCode != nil || domain.Decode(response.Result, &config) != nil || config.Config == nil || !currentTimeConfigEnabled(config.Config) {
		return domain.Fail(domain.Unsupported, "The Codex current-time profile could not be verified.", "Reconcile the native thread configuration before sending input.")
	}
	response, err = c.wire.Call(ctx, domain.NewID(), "experimentalFeature/list", struct {
		ThreadID domain.ID `json:"threadId"`
		Limit    int       `json:"limit"`
	}{ThreadID: c.thread, Limit: 256})
	if err != nil {
		return err
	}
	if response.ErrorCode != nil || !currentTimeFeatureEnabled(response.Result) {
		return domain.Fail(domain.Unsupported, "The Codex current-time profile could not be verified.", "Reconcile the native thread feature state before sending input.")
	}
	return nil
}

func (c *Client) currentTimeRequest(native nativewire.Event) (bool, error) {
	if !c.currentTimeEnabled || native.Kind != nativewire.ServerRequest || native.Method != "currentTime/read" || c.execution == nil {
		return false, nil
	}
	var params struct {
		ThreadID domain.ID `json:"threadId"`
	}
	if domain.DecodeWithLimit(native.Params, &params, 4096) != nil || params.ThreadID.Validate() != nil || native.Token.Validate() != nil {
		return false, incompatible()
	}
	if _, err := decodeNativeRequestID(native.ID); err != nil {
		return false, err
	}
	return params.ThreadID == c.thread && c.thread != "", nil
}

func (c *Client) replyCurrentTimeLocked(ctx context.Context, native nativewire.Event) (Event, error) {
	owned, err := c.currentTimeRequest(native)
	if err != nil {
		return Event{}, err
	}
	if !owned {
		return privateNative(native), nil
	}
	clock := c.currentTimeClock
	if clock == nil {
		clock = time.Now
	}
	err = c.wire.ReplyCurrentTime(ctx, native, clock)
	if c.logger != nil {
		delivery := currentTimeSent
		if err != nil {
			delivery = currentTimeUncertain
			if code := domain.SafeError(err).Code; code == domain.Conflict || code == domain.InvalidArgument {
				delivery = currentTimeRejected
			}
		}
		c.logger.InfoContext(ctx, "Codex native technical service response", "service_kind", "current-time", "delivery", delivery)
	}
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: CurrentTimeRepliedEvent, ThreadID: c.thread, Correlated: true}, nil
}
