package skill_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/skill"
	"github.com/soasurs/adk/tool"
)

func requireHandledError(t *testing.T, result *tool.Result, err error) *tool.HandledError {
	t.Helper()
	assert.Nil(t, result)
	var handledErr *tool.HandledError
	require.ErrorAs(t, err, &handledErr)
	return handledErr
}

func TestNewLoadTool_ReturnsStructuredSkill(t *testing.T) {
	catalog := loadTestCatalog(t)
	loadTool, err := skill.NewLoadTool(catalog, skill.WithLoadToolName("activate_skill"))
	require.NoError(t, err)
	assert.Equal(t, "activate_skill", loadTool.Definition().Name)
	assert.Equal(t, []any{"pdf-processing"}, loadTool.Definition().InputSchema.Properties["name"].Enum)

	result, runErr := loadTool.Run(t.Context(), tool.Call{
		Name:      "activate_skill",
		Arguments: json.RawMessage(`{"name":"pdf-processing"}`),
	})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	assert.JSONEq(t, `{
        "name": "pdf-processing",
        "description": "Process PDFs.",
        "compatibility": "Requires a PDF reader",
        "instructions": "# Instructions",
        "resources": ["references/guide.md"]
    }`, string(result.StructuredContent))
	assert.JSONEq(t, string(result.StructuredContent), result.Content)
}

func TestNewLoadTool_UnknownSkillIsHandled(t *testing.T) {
	loadTool, err := skill.NewLoadTool(loadTestCatalog(t))
	require.NoError(t, err)
	result, runErr := loadTool.Run(t.Context(), tool.Call{Arguments: json.RawMessage(`{"name":"missing"}`)})
	handledErr := requireHandledError(t, result, runErr)
	assert.JSONEq(t, `{"code":"skill_not_found","message":"skill \"missing\" is not available"}`, string(handledErr.StructuredContent))
}

func TestNewReadResourceTool_ReadsIndexedText(t *testing.T) {
	readTool, err := skill.NewReadResourceTool(loadTestCatalog(t), skill.WithReadResourceToolName("read_skill_file"))
	require.NoError(t, err)
	assert.Equal(t, "read_skill_file", readTool.Definition().Name)
	assert.Equal(t, []any{"pdf-processing"}, readTool.Definition().InputSchema.Properties["skill"].Enum)

	result, runErr := readTool.Run(t.Context(), tool.Call{Arguments: json.RawMessage(`{
        "skill":"pdf-processing",
        "path":"references/guide.md"
    }`)})
	require.NoError(t, runErr)
	require.NotNil(t, result)
	assert.JSONEq(t, `{
        "skill":"pdf-processing",
        "path":"references/guide.md",
        "content":"guide contents"
    }`, string(result.StructuredContent))
}

func TestNewReadResourceTool_ModelVisibleFailures(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/example/SKILL.md":      {Data: []byte("---\nname: example\ndescription: example skill\n---")},
		"skills/example/large.txt":     {Data: []byte("too large")},
		"skills/example/not-utf8.bin":  {Data: []byte{0xff, 0xfe}},
		"skills/example/reference.txt": {Data: []byte("reference")},
	}
	catalog, err := skill.Discover(fsys, "skills")
	require.NoError(t, err)
	readTool, err := skill.NewReadResourceTool(catalog, skill.WithMaxResourceBytes(4))
	require.NoError(t, err)

	tests := []struct {
		name      string
		arguments string
		code      string
	}{
		{name: "unknown skill", arguments: `{"skill":"missing","path":"reference.txt"}`, code: "skill_not_found"},
		{name: "traversal", arguments: `{"skill":"example","path":"../secret"}`, code: "invalid_resource_path"},
		{name: "unindexed", arguments: `{"skill":"example","path":"missing.txt"}`, code: "resource_not_found"},
		{name: "too large", arguments: `{"skill":"example","path":"large.txt"}`, code: "resource_too_large"},
		{name: "not utf8", arguments: `{"skill":"example","path":"not-utf8.bin"}`, code: "resource_not_utf8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, runErr := readTool.Run(t.Context(), tool.Call{Arguments: json.RawMessage(tt.arguments)})
			handledErr := requireHandledError(t, result, runErr)
			assert.Contains(t, string(handledErr.StructuredContent), tt.code)
		})
	}
}

func TestNewReadResourceTool_ParsedSkillHasNoResourceSource(t *testing.T) {
	parsed, err := skill.Parse([]byte("---\nname: example\ndescription: example skill\n---"))
	require.NoError(t, err)
	parsed.Resources = []string{"reference.txt"}
	catalog, err := skill.NewCatalog(parsed)
	require.NoError(t, err)
	readTool, err := skill.NewReadResourceTool(catalog)
	require.NoError(t, err)

	result, runErr := readTool.Run(t.Context(), tool.Call{Arguments: json.RawMessage(`{"skill":"example","path":"reference.txt"}`)})
	handledErr := requireHandledError(t, result, runErr)
	assert.Contains(t, string(handledErr.StructuredContent), "resources_unavailable")
}

func TestNewReadResourceTool_MissingIndexedFileIsTerminal(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/example/SKILL.md":     {Data: []byte("---\nname: example\ndescription: example skill\n---")},
		"skills/example/reference.md": {Data: []byte("reference")},
	}
	catalog, err := skill.Discover(fsys, "skills")
	require.NoError(t, err)
	delete(fsys, "skills/example/reference.md")
	readTool, err := skill.NewReadResourceTool(catalog)
	require.NoError(t, err)

	result, runErr := readTool.Run(t.Context(), tool.Call{Arguments: json.RawMessage(`{"skill":"example","path":"reference.md"}`)})
	assert.Empty(t, result)
	require.Error(t, runErr)
}

func TestLoad_DoesNotIndexSymlinks(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "example")
	require.NoError(t, os.Mkdir(skillDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: example\ndescription: example skill\n---"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0o644))
	if err := os.Symlink(filepath.Join(dir, "secret.txt"), filepath.Join(skillDir, "secret-link.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	loaded, err := skill.Load(os.DirFS(dir), "example")
	require.NoError(t, err)
	assert.Empty(t, loaded.Resources)
}

func TestSkillTools_ConcurrentReads(t *testing.T) {
	catalog := loadTestCatalog(t)
	loadTool, err := skill.NewLoadTool(catalog)
	require.NoError(t, err)
	readTool, err := skill.NewReadResourceTool(catalog)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, runErr := loadTool.Run(t.Context(), tool.Call{Arguments: json.RawMessage(`{"name":"pdf-processing"}`)})
			assert.NoError(t, runErr)
		}()
		go func() {
			defer wg.Done()
			_, runErr := readTool.Run(t.Context(), tool.Call{Arguments: json.RawMessage(`{"skill":"pdf-processing","path":"references/guide.md"}`)})
			assert.NoError(t, runErr)
		}()
	}
	wg.Wait()
}

func TestSkillTools_RejectEmptyCatalog(t *testing.T) {
	catalog, err := skill.NewCatalog()
	require.NoError(t, err)
	_, err = skill.NewLoadTool(catalog)
	require.Error(t, err)
	_, err = skill.NewReadResourceTool(catalog)
	require.Error(t, err)
}

func loadTestCatalog(t *testing.T) *skill.Catalog {
	t.Helper()
	fsys := fstest.MapFS{
		"skills/pdf-processing/SKILL.md": {Data: []byte(`---
name: pdf-processing
description: Process PDFs.
compatibility: Requires a PDF reader
---
# Instructions`)},
		"skills/pdf-processing/references/guide.md": {Data: []byte("guide contents")},
	}
	catalog, err := skill.Discover(fsys, "skills")
	require.NoError(t, err)
	return catalog
}
