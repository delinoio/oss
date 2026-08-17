package idgen

import "github.com/google/uuid"

type UUIDv7 struct{}

func (UUIDv7) New() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}
