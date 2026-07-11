package skill

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type frontmatter struct {
	Name          string         `yaml:"name"`
	Description   string         `yaml:"description"`
	License       string         `yaml:"license"`
	Compatibility string         `yaml:"compatibility"`
	Metadata      map[string]any `yaml:"metadata"`
	AllowedTools  any            `yaml:"allowed-tools"`
}

// Parse parses and validates one SKILL.md document. Directory-name validation
// and bundled-resource discovery are performed by Load.
func Parse(data []byte) (Skill, error) {
	frontmatterData, body, err := splitFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}

	var raw frontmatter
	if err := yaml.Unmarshal(frontmatterData, &raw); err != nil {
		return Skill{}, fmt.Errorf("skill: parse frontmatter: %w", err)
	}

	metadata := make(map[string]string, len(raw.Metadata))
	for key, value := range raw.Metadata {
		text, ok := value.(string)
		if !ok {
			return Skill{}, fmt.Errorf("skill: validate metadata.%s: value must be a string", key)
		}
		metadata[key] = text
	}
	if raw.Metadata == nil {
		metadata = nil
	}

	var allowedTools []string
	if raw.AllowedTools != nil {
		value, ok := raw.AllowedTools.(string)
		if !ok {
			return Skill{}, fmt.Errorf("skill: validate allowed-tools: must be a space-separated string")
		}
		allowedTools = strings.Fields(value)
	}

	skill := Skill{
		Name:          raw.Name,
		Description:   raw.Description,
		License:       raw.License,
		Compatibility: raw.Compatibility,
		Metadata:      metadata,
		AllowedTools:  allowedTools,
		Instructions:  strings.TrimSpace(string(body)),
	}
	if err := validateSkill(skill); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func splitFrontmatter(data []byte) ([]byte, []byte, error) {
	firstEnd := bytes.IndexByte(data, '\n')
	if firstEnd < 0 || string(bytes.TrimSuffix(data[:firstEnd], []byte{'\r'})) != "---" {
		return nil, nil, fmt.Errorf("skill: parse frontmatter: document must start with ---")
	}

	frontmatterStart := firstEnd + 1
	lineStart := frontmatterStart
	for lineStart <= len(data) {
		lineEnd := bytes.IndexByte(data[lineStart:], '\n')
		var line []byte
		var next int
		if lineEnd < 0 {
			line = data[lineStart:]
			next = len(data)
		} else {
			lineEnd += lineStart
			line = data[lineStart:lineEnd]
			next = lineEnd + 1
		}
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if string(line) == "---" {
			return data[frontmatterStart:lineStart], data[next:], nil
		}
		if lineEnd < 0 {
			break
		}
		lineStart = next
	}
	return nil, nil, fmt.Errorf("skill: parse frontmatter: closing --- not found")
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("skill: validate name: must not be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("skill: validate name: must not exceed 64 characters")
	}
	if !skillNamePattern.MatchString(name) {
		return fmt.Errorf("skill: validate name: must contain only lowercase letters, numbers, and single hyphens")
	}
	return nil
}

func runeCount(value string) int {
	return utf8.RuneCountInString(value)
}
