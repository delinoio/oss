// SPDX-License-Identifier: Apache-2.0
// Seeds an isolated disposable database; outputs secrets only to the harness pipe.
package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/store"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/proto"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "isolated load database setup/cleanup failed")
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := os.Getenv("AIPROUTER_TEST_DATABASE_URL")
	name := os.Getenv("AIPROUTER_LOAD_DATABASE_NAME")
	if root == "" || !regexp.MustCompile(`^aiprouter_load_[a-f0-9]{16}$`).MatchString(name) {
		return fmt.Errorf("invalid fixture database")
	}
	db, err := sql.Open("pgx", root)
	if err != nil {
		return err
	}
	defer db.Close()
	if len(os.Args) == 2 && os.Args[1] == "drop" {
		_, err = db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		return err
	}
	if _, err = db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		return err
	}
	u, err := url.Parse(root)
	if err != nil {
		return err
	}
	u.Path = "/" + name
	key, err := base64.StdEncoding.DecodeString(os.Getenv("AIPROUTER_LOAD_MASTER_KEY"))
	if err != nil {
		return err
	}
	envelope, err := core.NewEnvelope("v1", map[string][]byte{"v1": key})
	if err != nil {
		return err
	}
	operator := core.Identity{Issuer: "http://127.0.0.1:18090", Subject: "fixture-operator"}
	s, err := store.Open(ctx, u.String(), store.Options{Keys: envelope, Operators: []core.Identity{operator}})
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		return err
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	settings.RPM = 6000
	settings.Concurrency = 100
	settings.DestinationRules = []store.AllowedDestination{{Origin: "http://127.0.0.1:18090", AllowedCIDRs: []string{"127.0.0.1/32"}}}
	if err = s.UpdateSettings(ctx, operator, settings); err != nil {
		return err
	}
	var raw string
	for _, subject := range []string{"alice", "bob"} {
		user, e := s.RegisterIdentity(ctx, operator.Issuer, subject, subject+"@example.invalid", true)
		if e != nil {
			return e
		}
		for _, orgName := range []string{"alpha", "beta"} {
			org, e := s.CreateOrganization(ctx, user.ID, subject+"-"+orgName)
			if e != nil {
				return e
			}
			sc, _, e := s.Membership(ctx, org.ID, user.ID)
			if e != nil {
				return e
			}
			providerID := core.ID()
			data, e := json.Marshal(struct {
				Metadata *v1.ProviderConnection
				Secret   string
			}{&v1.ProviderConnection{Id: providerID, OrganizationId: org.ID, ProviderId: "custom", BaseUrl: "http://127.0.0.1:18090/v1", AuthKind: v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY, State: v1.ConnectionState_CONNECTION_STATE_READY}, "synthetic-provider-key"})
			if e != nil {
				return e
			}
			if e = s.PutPrivate(ctx, sc, "provider", providerID, data); e != nil {
				return e
			}
			route := &v1.Route{Id: core.ID(), OrganizationId: org.ID, ModelId: "fixture-model", Enabled: true, Targets: []*v1.RouteTarget{{Target: &v1.RouteTarget_ConnectionId{ConnectionId: providerID}, ModelId: "fixture-model"}}}
			data, e = proto.Marshal(route)
			if e != nil {
				return e
			}
			if e = s.PutPrivate(ctx, sc, "route", route.Id, data); e != nil {
				return e
			}
			_, issued, e := s.IssueKey(ctx, sc, "synthetic load", nil)
			if e != nil {
				return e
			}
			if raw == "" {
				raw = issued
			}
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"database_url": u.String(), "key": raw, "users": 2, "organizations": 4, "rpm": 6000, "concurrency_limit": 100})
}
