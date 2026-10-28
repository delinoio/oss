// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bufio"
	"context"
	"github.com/delinoio/oss/packages/aiprouter/store"
	serverconfig "github.com/delinoio/oss/servers/aiprouter/internal/config"
	observability "github.com/delinoio/oss/servers/aiprouter/internal/observability"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPInvitationDeliveryAndAcceptance(t *testing.T) {
	db := serverStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, _ := testIdentity(t, db, "mailowner")
	recipient, _ := testIdentity(t, db, "recipient")
	org, e := db.CreateOrganization(ctx, owner.ID, "Mail org")
	if e != nil {
		t.Fatal(e)
	}
	sc, _, e := db.Membership(ctx, org.ID, owner.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Invite(ctx, sc, recipient.Email); e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	message := make(chan string, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(4 * time.Second))
		r := bufio.NewReader(c)
		io.WriteString(c, "220 fixture ESMTP\r\n")
		for {
			line, e := r.ReadString('\n')
			if e != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				io.WriteString(c, "250 fixture\r\n")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				io.WriteString(c, "250 ok\r\n")
			case strings.HasPrefix(line, "DATA"):
				io.WriteString(c, "354 continue\r\n")
				var body strings.Builder
				for {
					line, e = r.ReadString('\n')
					if e != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				message <- body.String()
				io.WriteString(c, "250 queued\r\n")
			case strings.HasPrefix(line, "QUIT"):
				io.WriteString(c, "221 bye\r\n")
				return
			default:
				io.WriteString(c, "500 unsupported\r\n")
			}
		}
	}()
	s := &server{store: db, config: serverconfig.Config{SMTPAddr: listener.Addr().String(), SMTPFrom: "router@example.test", PublicURL: "https://router.example"}, telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))}
	mail, e := db.ClaimMail(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var token string
	if e = db.WithMailAttempt(ctx, mail, func(ctx context.Context, m store.Mail) store.MailOutcome { token = m.Token; return s.sendMail(ctx, m) }); e != nil {
		t.Fatal(e)
	}
	body := <-message
	if !strings.Contains(body, "https://router.example/invitation?token="+token) || !strings.Contains(body, "To: <recipient@example.test>") {
		t.Fatalf("invalid invitation fixture body")
	}
	if _, e = db.AcceptInvite(ctx, recipient.ID, token); e != nil {
		t.Fatal(e)
	}
}
