package snowflake

import (
	"sync"
	"testing"

	"github.com/bwmarrin/snowflake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ReturnsProcessGlobalNode(t *testing.T) {
	const callers = 32

	nodes := make([]*snowflake.Node, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			nodes[i], errs[i] = New()
		}(i)
	}
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Same(t, nodes[0], nodes[i])
	}
}
