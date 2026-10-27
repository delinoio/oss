// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func openConfig(path string) (*os.File, error) { return os.Open(path) }
func validateLinks(f *os.File) error {
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info) != nil || info.NumberOfLinks != 1 {
		return errors.New("shared or unverifiable configuration file; preserve its hard links")
	}
	return nil
}
