package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/pkg/thenv/api"
	"github.com/golang-jwt/jwt/v5"
)

type identity struct {
	Subject string
}

func (s *Server) authenticate(headers http.Header) (identity, error) {
	authorization := headers.Get("Authorization")
	if strings.TrimSpace(authorization) == "" {
		return identity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("missing authorization header"))
	}
	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authorization, bearerPrefix) {
		return identity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("authorization header must use bearer token"))
	}
	tokenValue := strings.TrimSpace(strings.TrimPrefix(authorization, bearerPrefix))
	if tokenValue == "" {
		return identity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("bearer token is empty"))
	}

	claims := jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenValue, &claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected token algorithm")
		}
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return identity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid token"))
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return identity{}, connect.NewError(connect.CodeUnauthenticated, errors.New("token subject is missing"))
	}
	return identity{Subject: claims.Subject}, nil
}

func (s *Server) authorizeAtLeast(ctx context.Context, actor identity, scope api.Scope, minimum api.Role) (api.Role, error) {
	if _, ok := s.superAdmins[actor.Subject]; ok {
		return api.RoleAdmin, nil
	}
	role, err := s.lookupRole(ctx, scope, actor.Subject)
	if err != nil {
		return api.RoleUnspecified, err
	}
	if !roleAtLeast(role, minimum) {
		return api.RoleUnspecified, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("role %s cannot access operation requiring %s", role.String(), minimum.String()))
	}
	return role, nil
}

func (s *Server) lookupRole(ctx context.Context, scope api.Scope, subject string) (api.Role, error) {
	query := `SELECT role FROM policy_bindings
	WHERE workspace_id = ? AND project_id = ? AND environment_id = ? AND subject = ?`
	var roleValue int
	err := s.db.QueryRowContext(ctx, query, scope.WorkspaceID, scope.ProjectID, scope.EnvironmentID, subject).Scan(&roleValue)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return api.RoleUnspecified, connect.NewError(connect.CodePermissionDenied, errors.New("no role binding for subject"))
		}
		return api.RoleUnspecified, connect.NewError(connect.CodeInternal, fmt.Errorf("lookup role: %w", err))
	}
	return api.Role(roleValue), nil
}

func roleAtLeast(current api.Role, minimum api.Role) bool {
	rank := map[api.Role]int{
		api.RoleReader: 1,
		api.RoleWriter: 2,
		api.RoleAdmin:  3,
	}
	return rank[current] >= rank[minimum]
}
