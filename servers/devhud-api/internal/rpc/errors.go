package rpc

import (
	"errors"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"google.golang.org/protobuf/proto"
)

func NewError(code connect.Code, message string, correlationID string, details ...proto.Message) *connect.Error {
	connectError := connect.NewError(code, errors.New(message))
	allDetails := append([]proto.Message{&devhudv1.ErrorMetadata{CorrelationId: uuid(correlationID)}}, details...)
	for _, message := range allDetails {
		detail, err := connect.NewErrorDetail(message)
		if err == nil {
			connectError.AddDetail(detail)
		}
	}
	return connectError
}

func metadata(correlationID string) *devhudv1.ResponseMetadata {
	return &devhudv1.ResponseMetadata{CorrelationId: uuid(correlationID)}
}

func uuid(value string) *devhudv1.UuidV7 { return &devhudv1.UuidV7{Value: value} }
