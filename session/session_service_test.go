package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListSessionsRequest_Normalize(t *testing.T) {
	t.Run("fills defaults", func(t *testing.T) {
		req, err := (ListSessionsRequest{}).Normalize()
		require.NoError(t, err)
		assert.Equal(t, int64(50), req.Limit)
		assert.Equal(t, SessionSortByCreatedAt, req.SortBy)
		assert.Equal(t, SortDescending, req.SortOrder)
	})

	tests := []struct {
		name string
		req  ListSessionsRequest
	}{
		{name: "negative limit", req: ListSessionsRequest{Limit: -1}},
		{name: "negative offset", req: ListSessionsRequest{Offset: -1}},
		{name: "unsupported sort field", req: ListSessionsRequest{SortBy: "unknown"}},
		{name: "unsupported sort order", req: ListSessionsRequest{SortOrder: "unknown"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.req.Normalize()
			assert.Error(t, err)
		})
	}
}
