// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// SharedRuntime owns one app-server and its original native process proof.
// Members own only thread state and never close the account connection.
type SharedRuntime struct {
	owner  *Client
	events *sharedEvents
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

// StartSharedRuntime transfers native event reading to the account owner.
// The caller must close the returned owner only after all member threads and
// retained descendants have been contained.
func (c *Client) StartSharedRuntime() (*SharedRuntime, error) {
	if c == nil || c.mode != ThreadProtocol || c.managedHome == "" || c.api != nil || c.thread != "" || c.sharedEvents != nil {
		return nil, incompatible()
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &SharedRuntime{owner: c, events: newSharedEvents(), cancel: cancel, done: make(chan struct{})}
	go runtime.read(ctx)
	return runtime, nil
}

func (s *SharedRuntime) read(ctx context.Context) {
	defer close(s.done)
	for {
		event, err := s.owner.wire.Next(ctx)
		if err != nil {
			s.events.fail(err)
			return
		}
		if event.Kind == nativewire.Notification && (event.Method == "account/updated" || event.Method == "account/rateLimits/updated") {
			if event.Method == "account/rateLimits/updated" && s.owner.quotaObserver != nil {
				var notification struct {
					RateLimits *nativeQuotaSnapshot `json:"rateLimits"`
				}
				if domain.Decode(event.Params, &notification) == nil && notification.RateLimits != nil {
					observed, observationErr := projectQuota(nativeQuotaRead{Legacy: notification.RateLimits}, domain.NewID(), time.Now().UTC())
					if observationErr == nil {
						observationErr = s.owner.validateQuotaReflection(observed)
					}
					if observationErr == nil {
						s.owner.quotaObserver(ctx, observed)
					} else if s.owner.logger != nil {
						s.owner.logger.WarnContext(ctx, "subscription_native_quota_rejected", "owner_id", s.owner.ownerID, "code", domain.SafeError(observationErr).Code)
					}
				}
			}
			continue
		}
		if event.Kind == nativewire.Notification && event.Method == "remoteControl/status/changed" {
			status, environment, decodeErr := decodeRemoteControl(event.Params)
			if decodeErr != nil || status != remoteControlDisabled || environment {
				s.events.fail(sharedEventUncertain())
				return
			}
			continue
		}
		if err := s.events.deliver(event); err != nil {
			s.events.fail(err)
			return
		}
	}
}

// NewMember creates an independent state machine on the account connection.
// Native startup options that cannot vary by thread must match the owner; the
// Worker must settle an incompatible member without sending its first input.
func (s *SharedRuntime) NewMember(config Config) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner := s.owner
	if s.closed || config.Mode != ThreadProtocol || !config.ManagedAuthentication || config.API != nil || config.Home != owner.home || config.Sidechat != owner.sidechat || config.ManagedTitle != owner.managedTitle || config.FrameProfile != DefaultFrameProfile || config.ModelObservation || config.CodeReviewModel != "" || config.RevertHistory != owner.revertHistory || config.EnableImageGeneration != owner.imageGeneration || config.WebSearchMode != owner.webSearchMode || config.ManagedForkHistory != owner.managedForkHistory {
		return nil, incompatible()
	}
	if err := s.events.check(); err != nil {
		return nil, err
	}
	return &Client{
		dynamicToolsEnabled: config.DynamicTools,
		managedForkHistory:  config.ManagedForkHistory,
		skillsRoot:          config.SkillsRoot,
		imageRoot:           config.ImageRoot,
		imageMachine:        config.ImageMachineID,
		sidechat:            config.Sidechat,
		home:                owner.home,
		wire:                owner.wire,
		sharedEvents:        s.events,
		version:             owner.version,
		ownerID:             config.Process.OwnerID,
		logger:              config.Process.Logger,
		control:             make(chan struct{}, 1),
		eventGate:           make(chan struct{}, 1),
		mode:                ThreadProtocol,
		managedHome:         owner.managedHome,
		managedTitle:        owner.managedTitle,
		revertHistory:       config.RevertHistory,
		imageGeneration:     config.EnableImageGeneration,
		webSearchMode:       config.WebSearchMode,
		imageObservations:   owner.imageObservations,
		reviewProtected:     security.NewProtectedJSON(config.Process.ProtectedValues),
	}, nil
}

// Close joins the original account process after the Worker has settled every
// member. An uncertain native join retains the account authentication owner.
func (s *SharedRuntime) Close() (nativeErr, helperErr error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return sharedEventUncertain(), nil
	}
	s.closed = true
	s.mu.Unlock()
	nativeErr, helperErr = s.owner.CloseWithExecutionHelpers()
	s.cancel()
	<-s.done
	return nativeErr, helperErr
}

// ManagedBundle reads the single account owner's current authentication while
// its native process is still open. A member never receives this bundle.
func (s *SharedRuntime) ManagedBundle(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, sharedEventUncertain()
	}
	return s.owner.ManagedBundle(ctx, false)
}

// Health reports loss of the account event connection without attempting a
// new native request or replaying any member input.
func (s *SharedRuntime) Health() error { return s.events.check() }

func (s *sharedEvents) check() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.problem
}

// call keeps late native replies attached to the member that issued them. A
// response with an unknown outcome remains tracked until the exact late reply
// arrives or the account owner is contained.
func (c *Client) call(ctx context.Context, request domain.ID, method string, params any) (nativewire.Response, error) {
	if c.sharedEvents != nil {
		if err := c.sharedEvents.check(); err != nil {
			return nativewire.Response{}, err
		}
	}
	tracked := c.sharedEvents != nil && c.thread != ""
	if tracked {
		if err := c.sharedEvents.trackRequest(request, c.thread); err != nil {
			return nativewire.Response{}, err
		}
	}
	response, err := c.wire.Call(ctx, request, method, params)
	if tracked && err == nil {
		c.sharedEvents.forgetRequest(request)
	}
	return response, err
}

// RetireSharedMember requires exact thread/unsubscribe and thread/closed
// evidence. It never joins or closes the account-owned process.
func (c *Client) RetireSharedMember(ctx context.Context, request domain.ID) error {
	if c.sharedEvents == nil || c.thread == "" || c.sharedQueue == nil {
		return sharedEventUncertain()
	}
	if err := c.UnsubscribeAndWait(ctx, request); err != nil {
		return err
	}
	c.sharedEvents.unbind(c.thread)
	c.sharedQueue = nil
	return nil
}
