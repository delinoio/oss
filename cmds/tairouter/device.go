// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

type Connector struct {
	API         ClientAPI
	Store       Credentials
	Clock       Clock
	Output      io.Writer
	OpenBrowser func(string) error
}

// Connect never changes client settings. Storage acknowledgement is sent only after
// the OS store has accepted the key. All remote and store errors are value-free.
func (c Connector) Connect(ctx context.Context, p Profile, deviceName string) (Profile, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	if c.Clock == nil {
		c.Clock = RealClock{}
	}
	// Probe the actual credential store without risking an existing profile value.
	probe := "probe/" + p.Name
	if err := c.Store.Set(probe, "tairouter-availability-check"); err != nil {
		return p, ErrUnavailable
	}
	if err := c.Store.Delete(probe); err != nil {
		return p, ErrUnavailable
	}
	started := c.Clock.Now()
	d, err := c.API.Begin(ctx, deviceName)
	if err != nil {
		return p, errors.New("device authorization could not start")
	}
	origin, _ := url.Parse(p.Server)
	approval, e := url.Parse(d.URL)
	if e != nil || approval.Scheme != origin.Scheme || approval.Host != origin.Host || approval.User != nil || strings.Contains(d.URL, d.Secret) || strings.ContainsAny(d.Code, "\r\n\x1b") || len(d.Code) > 128 || d.Secret == "" || d.ID == "" || d.Code == "" || !d.Expires.After(started) || d.Expires.After(started.Add(10*time.Minute+time.Second)) {
		return p, errors.New("server returned an invalid device authorization")
	}
	fmt.Fprintf(c.Output, "Open %s\nApproval code: %s\n", d.URL, d.Code)
	if c.OpenBrowser != nil {
		if c.OpenBrowser(d.URL) != nil {
			fmt.Fprintln(c.Output, "Open the URL in a browser on another device to continue.")
		}
	}
	for c.Clock.Now().Before(d.Expires) {
		if err := c.Clock.Wait(ctx, PollInterval); err != nil {
			return p, err
		}
		if !c.Clock.Now().Before(d.Expires) {
			break
		}
		g, err := c.API.Poll(ctx, d)
		if err != nil {
			if ctx.Err() != nil {
				return p, ctx.Err()
			}
			continue
		}
		switch g.Status {
		case DevicePending:
			continue
		case DeviceDenied:
			return p, errors.New("device approval denied")
		case DeviceExpired:
			return p, errors.New("device authorization expired")
		case DeviceApproved:
			if g.Key == "" || g.KeyID == "" || g.Organization == "" {
				return p, errors.New("invalid device grant")
			}
			p.KeyID = g.KeyID
			p.Organization = g.Organization
			if c.Store.Set(credentialID(p), g.Key) != nil {
				revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
				defer cancel()
				if c.API.Revoke(revokeCtx, g.KeyID, g.Key) != nil {
					return p, fmt.Errorf("%w; server revocation incomplete. %s", ErrUnavailable, revokeHint(p))
				}
				return p, ErrUnavailable
			}
			// Retry acknowledgement within the original lifetime. Retaining the same OS
			// entry allows a lost response to be retried without issuing another key.
			for c.Clock.Now().Before(d.Expires) {
				if c.API.Confirm(ctx, d) == nil {
					return p, nil
				}
				if c.Clock.Wait(ctx, PollInterval) != nil {
					break
				}
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			revokeErr := c.API.Revoke(cleanupCtx, g.KeyID, g.Key)
			deleteErr := c.Store.Delete(credentialID(p))
			if revokeErr != nil {
				return p, fmt.Errorf("storage confirmation incomplete; server revocation incomplete. %s", revokeHint(p))
			}
			if deleteErr != nil {
				return p, errors.New("storage confirmation failed; revoked key remains in OS store; remove it manually")
			}
			return p, errors.New("storage confirmation failed; key revoked")
		default:
			return p, errors.New("unknown device authorization status")
		}
	}
	return p, errors.New("device authorization expired")
}
