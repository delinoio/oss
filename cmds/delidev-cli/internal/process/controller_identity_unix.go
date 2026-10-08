// SPDX-License-Identifier: Apache-2.0
//go:build darwin || linux

package process

func controllerBirth(pid int) (string, error)                    { return preciseBirth(pid) }
func controllerPresent(ControllerIdentity) ControllerObservation { return ControllerAlive }
