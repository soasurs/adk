package skill

import (
	"encoding/json"
	"fmt"
	"strings"
)

type catalogInstruction struct {
	Usage  catalogUsage   `json:"usage"`
	Skills []catalogSkill `json:"skills"`
}

type catalogUsage struct {
	Tool        string `json:"tool"`
	Instruction string `json:"instruction"`
}

type catalogSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Instruction renders the catalog for request-scoped injection. It returns an
// empty string when the catalog contains no skills.
func (c *Catalog) Instruction(configure ...Option) (string, error) {
	configured, err := applyOptions(configure...)
	if err != nil {
		return "", err
	}
	if c == nil || len(c.skills) == 0 {
		return "", nil
	}

	switch configured.instructionFormat {
	case InstructionFormatText:
		return c.textInstruction(configured.loadToolName), nil
	case InstructionFormatJSON:
		return c.jsonInstruction(configured.loadToolName)
	default:
		return "", fmt.Errorf("skill: unsupported instruction format %q", configured.instructionFormat)
	}
}

func (c *Catalog) textInstruction(loadToolName string) string {
	var instruction strings.Builder
	instruction.WriteString("The following skills provide specialized instructions:\n\n")
	for _, skill := range c.skills {
		fmt.Fprintf(&instruction, "- %s: %s\n", skill.Name, strings.Join(strings.Fields(skill.Description), " "))
	}
	fmt.Fprintf(&instruction, "\nWhen a task matches a skill, call %s with its name before proceeding.", loadToolName)
	return instruction.String()
}

func (c *Catalog) jsonInstruction(loadToolName string) (string, error) {
	skills := make([]catalogSkill, len(c.skills))
	for i, skill := range c.skills {
		skills[i] = catalogSkill{Name: skill.Name, Description: skill.Description}
	}
	data, err := json.Marshal(catalogInstruction{
		Usage: catalogUsage{
			Tool:        loadToolName,
			Instruction: "Load a matching skill before proceeding.",
		},
		Skills: skills,
	})
	if err != nil {
		return "", fmt.Errorf("skill: render catalog instruction: %w", err)
	}
	return string(data), nil
}
