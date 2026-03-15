package tool

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

type echo struct{}

type echoRequest struct {
	Request string `json:"echo" jsonschema:"request message, e.g. Hello World!"`
}

func NewEchoTool() Tool {
	return &echo{}
}

func (e *echo) Name() string {
	return "Echo"
}

func (e *echo) Description() string {
	return "A tool to echo request message."
}

func (e *echo) InputSchema() (*jsonschema.Schema, error) {
	requestType := reflect.TypeFor[echoRequest]()
	return jsonschema.ForType(requestType, &jsonschema.ForOptions{})
}

func (e *echo) Run(_ context.Context, _ string, arguments string) (string, error) {
	request := new(echoRequest)
	if err := json.Unmarshal([]byte(arguments), request); err != nil {
		return "", err
	}
	return request.Request, nil
}
