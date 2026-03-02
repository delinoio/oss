package compiler

import "context"

type CheckOptions struct {
	Entry string
}

type BuildOptions struct {
	Entry  string
	OutDir string
}

type ExplainOptions struct {
	Entry string
	Task  string
}

type Result struct {
	Entry       string
	Module      string
	Diagnostics []string
}

type Service struct{}

func New() *Service {
	return &Service{}
}

func (s *Service) Check(_ context.Context, options CheckOptions) (Result, error) {
	return Result{Entry: options.Entry}, nil
}

func (s *Service) Build(_ context.Context, options BuildOptions) (Result, error) {
	return Result{Entry: options.Entry}, nil
}

func (s *Service) Explain(_ context.Context, options ExplainOptions) (Result, error) {
	return Result{Entry: options.Entry}, nil
}
