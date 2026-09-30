// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"flag"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func DefaultDataDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "delidev"), nil
}

func ensureRequest(o *options) {
	if o.requestID == "" {
		o.requestID = domain.NewID()
	}
}

func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	return f
}

func parse(f *flag.FlagSet, args []string) error {
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return usage()
	}
	return nil
}

func usage() error {
	return domain.Fail(domain.InvalidArgument, "Invalid command or arguments.", "Run `delidev help` for supported command syntax.")
}

func globals(args []string) (options, []string, error) {
	var o options
	rest := []string{}
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		switch name {
		case "--json":
			if inline && value != "true" {
				return o, nil, usage()
			}
		case "--token-stdin":
			if inline {
				return o, nil, usage()
			}
			o.tokenStdin = true
		case "--data-dir", "--server", "--request-id":
			if !inline {
				i++
				if i >= len(args) {
					return o, nil, usage()
				}
				value = args[i]
			}
			if value == "" {
				return o, nil, usage()
			}
			switch name {
			case "--data-dir":
				o.dataDir = value
			case "--server":
				o.server = value
			case "--request-id":
				o.requestID = domain.ID(value)
				if err := o.requestID.Validate(); err != nil {
					return o, nil, err
				}
			}
		default:
			rest = append(rest, args[i])
		}
	}
	return o, rest, nil
}
