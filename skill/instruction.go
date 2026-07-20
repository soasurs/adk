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
		return c.textInstruction(configured), nil
	case InstructionFormatJSON:
		return c.jsonInstruction(configured)
	default:
		return "", fmt.Errorf("skill: unsupported instruction format %q", configured.instructionFormat)
	}
}

func (c *Catalog) textInstruction(configured options) string {
	var instruction strings.Builder
	instruction.WriteString("The following skills provide specialized instructions:\n\n")
	for _, skill := range c.skills {
		fmt.Fprintf(&instruction, "- %s: %s\n", skill.Name, strings.Join(strings.Fields(skill.Description), " "))
	}
	usage := configured.usageInstruction
	if usage == "" {
		usage = fmt.Sprintf("When a task matches a skill, call %s with its name before proceeding.", configured.loadToolName)
	}
	fmt.Fprintf(&instruction, "\n%s", usage)
	return instruction.String()
}

func (c *Catalog) jsonInstruction(configured options) (string, error) {
	skills := make([]catalogSkill, len(c.skills))
	for i, skill := range c.skills {
		skills[i] = catalogSkill{Name: skill.Name, Description: skill.Description}
	}
	usageInstruction := configured.usageInstruction
	if usageInstruction == "" {
		usageInstruction = "Load a matching skill before proceeding."
	}
	data, err := json.Marshal(catalogInstruction{
		Usage: catalogUsage{
			Tool:        configured.loadToolName,
			Instruction: usageInstruction,
		},
		Skills: skills,
	})
	if err != nil {
		return "", fmt.Errorf("skill: render catalog instruction: %w", err)
	}
	return string(data), nil
}
