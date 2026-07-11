package skill_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/skill"
)

func TestLoad_IndexesResourcesAndValidatesDirectory(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/pdf-processing/SKILL.md":        {Data: []byte("---\nname: pdf-processing\ndescription: Process PDFs.\n---\nInstructions")},
		"skills/pdf-processing/references/z.md": {Data: []byte("z")},
		"skills/pdf-processing/assets/a.txt":    {Data: []byte("a")},
	}

	loaded, err := skill.Load(fsys, "skills/pdf-processing")
	require.NoError(t, err)
	assert.Equal(t, []string{"assets/a.txt", "references/z.md"}, loaded.Resources)

	_, err = skill.Load(fsys, "skills/wrong-name")
	require.Error(t, err)
}

func TestLoad_NameMustMatchDirectory(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/other/SKILL.md": {Data: []byte("---\nname: simple\ndescription: useful\n---")},
	}

	_, err := skill.Load(fsys, "skills/other")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must match directory name")
}

func TestDiscover_LoadsDirectChildrenAndSortsByName(t *testing.T) {
	fsys := fstest.MapFS{
		"skills/zeta/SKILL.md":             {Data: []byte("---\nname: zeta\ndescription: zeta skill\n---")},
		"skills/alpha/SKILL.md":            {Data: []byte("---\nname: alpha\ndescription: alpha skill\n---")},
		"skills/not-a-skill/README.md":     {Data: []byte("ignored")},
		"skills/group/nested/SKILL.md":     {Data: []byte("---\nname: nested\ndescription: nested skill\n---")},
		"second/beta/SKILL.md":             {Data: []byte("---\nname: beta\ndescription: beta skill\n---")},
		"second/beta/references/detail.md": {Data: []byte("detail")},
	}

	catalog, err := skill.Discover(fsys, "skills", "second")
	require.NoError(t, err)
	available := catalog.Skills()
	require.Len(t, available, 3)
	assert.Equal(t, []string{"alpha", "beta", "zeta"}, []string{available[0].Name, available[1].Name, available[2].Name})
}

func TestNewCatalog_RejectsDuplicateNames(t *testing.T) {
	candidate := skill.Skill{Name: "simple", Description: "A simple skill."}
	_, err := skill.NewCatalog(candidate, candidate)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate skill name")
}

func TestNewCatalog_RejectsInvalidResources(t *testing.T) {
	_, err := skill.NewCatalog(skill.Skill{
		Name:        "simple",
		Description: "A simple skill.",
		Resources:   []string{"../secret"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource path")
}

func TestCatalog_SkillsReturnsIsolatedCopy(t *testing.T) {
	catalog, err := skill.NewCatalog(skill.Skill{
		Name:         "simple",
		Description:  "A simple skill.",
		Metadata:     map[string]string{"author": "original"},
		AllowedTools: []string{"Read"},
		Resources:    []string{"reference.md"},
	})
	require.NoError(t, err)

	copy := catalog.Skills()
	copy[0].Name = "changed"
	copy[0].Metadata["author"] = "changed"
	copy[0].AllowedTools[0] = "Changed"
	copy[0].Resources[0] = "changed.md"

	available := catalog.Skills()
	assert.Equal(t, "simple", available[0].Name)
	assert.Equal(t, "original", available[0].Metadata["author"])
	assert.Equal(t, "Read", available[0].AllowedTools[0])
	assert.Equal(t, "reference.md", available[0].Resources[0])
}
