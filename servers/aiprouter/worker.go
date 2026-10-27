// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/delinoio/oss/packages/aiprouter/store"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"time"
)

func (s *server) cleanup(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run, cancel := context.WithTimeout(ctx, 50*time.Second)
			e := s.store.Cleanup(run)
			if e == nil && s.artifacts != nil {
				e = s.artifacts.Cleanup(run)
			}
			cancel()
			if e != nil {
				s.telemetry.cleanupFailures.Inc()
				s.telemetry.logger.ErrorContext(ctx, "cleanup failed")
			} else {
				s.telemetry.cleanupFailures.Set(0)
				s.telemetry.cleanupSuccess.SetToCurrentTime()
			}
		}
	}
}
func (s *server) mailWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run, cancel := context.WithTimeout(ctx, 30*time.Second)
			m, e := s.store.ClaimMail(run)
			if e == nil {
				e = s.store.WithMailAttempt(run, m, s.sendMail)
			}
			cancel()
			if e != nil && !errors.Is(e, store.ErrNotFound) && ctx.Err() == nil {
				s.telemetry.logger.ErrorContext(ctx, "invitation delivery worker failed")
			}
		}
	}
}
func (s *server) sendMail(ctx context.Context, m store.Mail) store.MailOutcome {
	e := s.smtp(ctx, m)
	if e == nil {
		return store.MailSuccess
	}
	var smtpErr *textproto.Error
	if errors.As(e, &smtpErr) && smtpErr.Code >= 500 {
		return store.MailPermanent
	}
	return store.MailTransient
}
func (s *server) smtp(ctx context.Context, m store.Mail) error {
	from, e := mail.ParseAddress(s.config.SMTPFrom)
	if e != nil {
		return e
	}
	to, e := mail.ParseAddress(m.Email)
	if e != nil {
		return &textproto.Error{Code: 550, Msg: "invalid recipient"}
	}
	host, _, e := net.SplitHostPort(s.config.SMTPAddr)
	if e != nil {
		return e
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, e := dialer.DialContext(ctx, "tcp", s.config.SMTPAddr)
	if e != nil {
		return e
	}
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	client, e := smtp.NewClient(conn, host)
	if e != nil {
		return e
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if e = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); e != nil {
			return e
		}
	} else if !isLoopback(host) {
		return errors.New("SMTP TLS required")
	}
	if s.config.SMTPUsername != "" {
		if e = client.Auth(smtp.PlainAuth("", s.config.SMTPUsername, s.config.SMTPPassword, host)); e != nil {
			return e
		}
	}
	if e = client.Mail(from.Address); e != nil {
		return e
	}
	if e = client.Rcpt(to.Address); e != nil {
		return e
	}
	writer, e := client.Data()
	if e != nil {
		return e
	}
	link := s.config.PublicURL + "/invitation?token=" + url.QueryEscape(m.Token)
	_, e = fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: AIPRouter organization invitation\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nSign in with your verified email to accept this invitation:\r\n%s\r\n\r\nThis invitation expires in seven days.\r\n", from.String(), to.String(), link)
	if e != nil {
		return e
	}
	if e = writer.Close(); e != nil {
		return e
	}
	return client.Quit()
}
