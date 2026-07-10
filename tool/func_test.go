package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/tool"
)

type funcInput struct {
	Message string `json:"message"`
}

type funcOutput struct {
	Echo string `json:"echo"`
}

func TestNewFunc_Run_StructuredResult(t *testing.T) {
	echo, err := tool.NewFunc(tool.Definition{
		Name:        "echo",
		Description: "echo a message",
	}, func(_ context.Context, input funcInput) (funcOutput, error) {
		return funcOutput{Echo: input.Message}, nil
	})
	require.NoError(t, err)

	def := echo.Definition()
	require.NotNil(t, def.InputSchema)
	require.NotNil(t, def.OutputSchema)

	result, err := echo.Run(t.Context(), tool.Call{
		ID:        "call-1",
		Name:      "echo",
		Arguments: json.RawMessage(`{"message":"hello"}`),
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.JSONEq(t, `{"echo":"hello"}`, string(result.StructuredContent))
	assert.JSONEq(t, `{"echo":"hello"}`, result.Content)
}

func TestNewFunc_Run_StringResultUsesPlainTextContent(t *testing.T) {
	echo, err := tool.NewFunc(tool.Definition{Name: "echo"}, func(_ context.Context, input funcInput) (string, error) {
		return input.Message, nil
	})
	require.NoError(t, err)

	result, err := echo.Run(t.Context(), tool.Call{
		ID:        "call-1",
		Name:      "echo",
		Arguments: json.RawMessage(`{"message":"hello"}`),
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "hello", result.Content)
	assert.JSONEq(t, `"hello"`, string(result.StructuredContent))
}

func TestNewFunc_Run_HandlerErrorPropagates(t *testing.T) {
	handlerErr := errors.New("not available")
	failing, err := tool.NewFunc(tool.Definition{Name: "fail"}, func(_ context.Context, input funcInput) (funcOutput, error) {
		return funcOutput{}, handlerErr
	})
	require.NoError(t, err)

	result, runErr := failing.Run(t.Context(), tool.Call{
		ID:        "call-1",
		Name:      "fail",
		Arguments: json.RawMessage(`{"message":"hello"}`),
	})

	assert.Empty(t, result)
	require.ErrorIs(t, runErr, handlerErr)
	assert.Contains(t, runErr.Error(), `tool "fail": run handler`)
}

func TestNewFunc_Run_FuncErrorReturnsModelVisibleFailure(t *testing.T) {
	failing, err := tool.NewFunc(tool.Definition{Name: "fail"}, func(_ context.Context, input funcInput) (funcOutput, error) {
		funcErr := tool.NewFuncError("not available")
		funcErr.StructuredContent = json.RawMessage(`{"code":"not_available"}`)
		return funcOutput{}, funcErr
	})
	require.NoError(t, err)

	result, runErr := failing.Run(t.Context(), tool.Call{
		ID:        "call-1",
		Name:      "fail",
		Arguments: json.RawMessage(`{"message":"hello"}`),
	})

	require.NoError(t, runErr)
	assert.True(t, result.IsError)
	assert.Equal(t, "not available", result.Content)
	assert.JSONEq(t, `{"code":"not_available"}`, string(result.StructuredContent))
}

func TestNewFunc_Run_InvalidArgumentsReturnModelVisibleFailure(t *testing.T) {
	called := false
	failing, err := tool.NewFunc(tool.Definition{Name: "fail"}, func(_ context.Context, input funcInput) (funcOutput, error) {
		called = true
		return funcOutput{}, nil
	})
	require.NoError(t, err)

	result, runErr := failing.Run(t.Context(), tool.Call{
		ID:        "call-1",
		Name:      "fail",
		Arguments: json.RawMessage(`{"message":`),
	})

	require.NoError(t, runErr)
	assert.False(t, called)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "parse arguments")
}
