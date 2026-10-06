// SPDX-License-Identifier: Apache-2.0
package domain

const MaxCompactionInputBytes = 3 << 20
const MaxCompactionJobBytes = 4 << 20

func DecodeCompactionInput(raw []byte, target *SessionCompactionInput) error {
	return DecodeWithLimit(raw, target, MaxCompactionInputBytes)
}

func DecodeCompactionJob(raw []byte, target *Job) error {
	if err := DecodeWithLimit(raw, target, MaxCompactionJobBytes); err != nil {
		return err
	}
	var input SessionCompactionInput
	if target.Type != CompactSessionJob || target.Validate() != nil || DecodeCompactionInput(target.Input, &input) != nil || input.Validate() != nil {
		return CompactionUncertain()
	}
	return nil
}
