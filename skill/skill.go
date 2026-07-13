package skill

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
)

// Skill is one parsed Agent Skill.
type Skill struct {
	Name          string
	Description   string
	License       string
	Compatibility string
	Metadata      map[string]string
	AllowedTools  []string
	Instructions  string
	Resources     []string

	source *source
}

type source struct {
	fsys      fs.FS
	directory string
	resources map[string]struct{}
}

func cloneSkill(skill Skill) Skill {
	cloned := skill
	cloned.Metadata = cloneMap(skill.Metadata)
	cloned.AllowedTools = slices.Clone(skill.AllowedTools)
	cloned.Resources = slices.Clone(skill.Resources)
	return cloned
}

func cloneMap[K comparable, V any](input map[K]V) map[K]V {
	if input == nil {
		return nil
	}
	cloned := make(map[K]V, len(input))
	maps.Copy(cloned, input)
	return cloned
}

func validateSkill(skill Skill) error {
	if err := validateName(skill.Name); err != nil {
		return err
	}
	if strings.TrimSpace(skill.Description) == "" {
		return fmt.Errorf("skill: validate description: must not be empty")
	}
	if runeCount(skill.Description) > 1024 {
		return fmt.Errorf("skill: validate description: must not exceed 1024 characters")
	}
	if skill.Compatibility != "" && runeCount(skill.Compatibility) > 500 {
		return fmt.Errorf("skill: validate compatibility: must not exceed 500 characters")
	}
	seenResources := make(map[string]struct{}, len(skill.Resources))
	for _, resource := range skill.Resources {
		if !fs.ValidPath(resource) || resource == "." || resource == "SKILL.md" {
			return fmt.Errorf("skill: validate resource path %q: must be a valid bundled file path", resource)
		}
		if _, exists := seenResources[resource]; exists {
			return fmt.Errorf("skill: validate resource path %q: must not be duplicated", resource)
		}
		seenResources[resource] = struct{}{}
	}
	return nil
}
