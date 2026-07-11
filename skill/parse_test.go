package skill_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/skill"
)

func TestParse_AllFields(t *testing.T) {
	document := `---
name: pdf-processing
description: Extract and process PDF documents.
license: Apache-2.0
compatibility: Requires Python 3
metadata:
  author: example
  version: "1.0"
allowed-tools: Bash(git:*) Read
---

# PDF Processing

Follow these instructions.
`

	parsed, err := skill.Parse([]byte(document))
	require.NoError(t, err)
	assert.Equal(t, "pdf-processing", parsed.Name)
	assert.Equal(t, "Extract and process PDF documents.", parsed.Description)
	assert.Equal(t, "Apache-2.0", parsed.License)
	assert.Equal(t, "Requires Python 3", parsed.Compatibility)
	assert.Equal(t, map[string]string{"author": "example", "version": "1.0"}, parsed.Metadata)
	assert.Equal(t, []string{"Bash(git:*)", "Read"}, parsed.AllowedTools)
	assert.Equal(t, "# PDF Processing\n\nFollow these instructions.", parsed.Instructions)
}

func TestParse_CRLFAndEmptyBody(t *testing.T) {
	parsed, err := skill.Parse([]byte("---\r\nname: simple\r\ndescription: A simple skill.\r\n---\r\n"))
	require.NoError(t, err)
	assert.Empty(t, parsed.Instructions)
}

func TestParse_InvalidDocuments(t *testing.T) {
	tests := []struct {
		name     string
		document string
		contains string
	}{
		{name: "missing opening delimiter", document: "name: simple", contains: "must start with ---"},
		{name: "missing closing delimiter", document: "---\nname: simple", contains: "closing --- not found"},
		{name: "invalid yaml", document: "---\nname: [\n---", contains: "parse frontmatter"},
		{name: "missing name", document: "---\ndescription: useful\n---", contains: "name: must not be empty"},
		{name: "invalid name", document: "---\nname: PDF--tool\ndescription: useful\n---", contains: "lowercase letters"},
		{name: "empty description", document: "---\nname: simple\ndescription: '   '\n---", contains: "description: must not be empty"},
		{name: "long description", document: "---\nname: simple\ndescription: " + strings.Repeat("x", 1025) + "\n---", contains: "1024 characters"},
		{name: "long compatibility", document: "---\nname: simple\ndescription: useful\ncompatibility: " + strings.Repeat("x", 501) + "\n---", contains: "500 characters"},
		{name: "non-string metadata", document: "---\nname: simple\ndescription: useful\nmetadata:\n  version: 1\n---", contains: "metadata.version"},
		{name: "non-string allowed tools", document: "---\nname: simple\ndescription: useful\nallowed-tools: [Read]\n---", contains: "space-separated string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := skill.Parse([]byte(tt.document))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.contains)
		})
	}
}
