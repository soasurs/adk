package builtin_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/tool"
	"github.com/soasurs/adk/tool/builtin"
)

func TestEchoTool_Run_InvalidArgumentsReturnModelVisibleFailure(t *testing.T) {
	echo, err := builtin.NewEchoTool()
	require.NoError(t, err)

	result, runErr := echo.Run(t.Context(), tool.Call{
		ID:        "call-1",
		Name:      "Echo",
		Arguments: json.RawMessage(`{"echo":`),
	})

	assert.Nil(t, result)
	var handledErr *tool.HandledError
	require.ErrorAs(t, runErr, &handledErr)
	assert.Contains(t, handledErr.Content, "parse arguments")
}
