package skill_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/skill"
)

func TestCatalog_InstructionText(t *testing.T) {
	catalog, err := skill.NewCatalog(
		skill.Skill{Name: "zeta", Description: "Zeta\n skill."},
		skill.Skill{Name: "alpha", Description: "Alpha skill."},
	)
	require.NoError(t, err)

	instruction, err := catalog.Instruction(skill.WithLoadToolName("activate_skill"))
	require.NoError(t, err)
	assert.Equal(t, `The following skills provide specialized instructions:

- alpha: Alpha skill.
- zeta: Zeta skill.

When a task matches a skill, call activate_skill with its name before proceeding.`, instruction)
}

func TestCatalog_InstructionJSON(t *testing.T) {
	catalog, err := skill.NewCatalog(skill.Skill{Name: "simple", Description: "A simple skill."})
	require.NoError(t, err)

	instruction, err := catalog.Instruction(
		skill.WithInstructionFormat(skill.InstructionFormatJSON),
		skill.WithLoadToolName("activate_skill"),
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{
        "usage": {
            "tool": "activate_skill",
            "instruction": "Load a matching skill before proceeding."
        },
        "skills": [{
            "name": "simple",
            "description": "A simple skill."
        }]
    }`, instruction)
}

func TestCatalog_InstructionEmpty(t *testing.T) {
	catalog, err := skill.NewCatalog()
	require.NoError(t, err)
	instruction, err := catalog.Instruction()
	require.NoError(t, err)
	assert.Empty(t, instruction)
}

func TestCatalog_InstructionRejectsInvalidOptions(t *testing.T) {
	catalog, err := skill.NewCatalog(skill.Skill{Name: "simple", Description: "A simple skill."})
	require.NoError(t, err)

	_, err = catalog.Instruction(skill.WithInstructionFormat("yaml"))
	require.Error(t, err)
	_, err = catalog.Instruction(skill.WithLoadToolName(""))
	require.Error(t, err)
}

func TestCatalog_JSONInstructionIsValidJSON(t *testing.T) {
	catalog, err := skill.NewCatalog(skill.Skill{Name: "simple", Description: "quotes: \" and newline\n"})
	require.NoError(t, err)
	instruction, err := catalog.Instruction(skill.WithInstructionFormat(skill.InstructionFormatJSON))
	require.NoError(t, err)
	assert.True(t, json.Valid([]byte(instruction)))
}
