//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package shell

// POSIX permission bits are recorded and checked by Plan.
type fileSecurity struct{}

func readSecurity(string) (*fileSecurity, error)   { return &fileSecurity{}, nil }
func (*fileSecurity) matches(string) (bool, error) { return true, nil }
func (*fileSecurity) preserve(string) error        { return nil }
