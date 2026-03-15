package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/soasurs/adk/tool"
)

type echo struct {
	def tool.Definition
}

type echoRequest struct {
	Request string `json:"echo" jsonschema:"request message, e.g. Hello World!"`
}

func NewEchoTool() tool.Tool {
	schema, err := jsonschema.ForType(reflect.TypeFor[echoRequest](), &jsonschema.ForOptions{})
	if err != nil {
		panic(fmt.Sprintf("echo: build input schema: %v", err))
	}
	return &echo{
		def: tool.Definition{
			Name:        "Echo",
			Description: "A tool to echo request message.",
			InputSchema: schema,
		},
	}
}

func (e *echo) Definition() tool.Definition {
	return e.def
}

func (e *echo) Run(_ context.Context, _ string, arguments string) (string, error) {
	request := new(echoRequest)
	if err := json.Unmarshal([]byte(arguments), request); err != nil {
		return "", err
	}
	return request.Request, nil
}
