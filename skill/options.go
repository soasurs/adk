package skill

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	defaultLoadToolName         = "load_skill"
	defaultReadResourceToolName = "read_skill_resource"
	defaultMaxResourceBytes     = int64(256 * 1024)
)

// InstructionFormat controls how Catalog.Instruction renders the catalog.
type InstructionFormat string

const (
	// InstructionFormatText renders a compact, human-readable instruction.
	InstructionFormatText InstructionFormat = "text"
	// InstructionFormatJSON renders a machine-structured JSON instruction.
	InstructionFormatJSON InstructionFormat = "json"
)

// Option configures catalog instruction rendering and skill tools. The same
// options can be shared across Instruction, NewLoadTool, and
// NewReadResourceTool so tool names remain consistent.
type Option func(*options)

type options struct {
	instructionFormat    InstructionFormat
	loadToolName         string
	readResourceToolName string
	maxResourceBytes     int64

	// usageInstruction overrides the default instruction text that tells
	// the model how to decide when to invoke the load-skill tool. An empty
	// string keeps the format-specific default.
	usageInstruction string
}

// WithInstructionFormat selects text or JSON catalog rendering.
func WithInstructionFormat(format InstructionFormat) Option {
	return func(options *options) {
		options.instructionFormat = format
	}
}

// WithLoadToolName overrides the default load_skill tool name.
func WithLoadToolName(name string) Option {
	return func(options *options) {
		options.loadToolName = name
	}
}

// WithReadResourceToolName overrides the default read_skill_resource tool
// name.
func WithReadResourceToolName(name string) Option {
	return func(options *options) {
		options.readResourceToolName = name
	}
}

// WithMaxResourceBytes sets the maximum text resource size returned by
// NewReadResourceTool. The limit must be positive.
func WithMaxResourceBytes(limit int64) Option {
	return func(options *options) {
		options.maxResourceBytes = limit
	}
}

// WithUsageInstruction replaces the default instruction text appended
// after the skill list. The text should describe when the model should
// call the load-skill tool. An empty or whitespace-only value keeps
// the default.
func WithUsageInstruction(text string) Option {
	return func(opts *options) {
		if strings.TrimSpace(text) == "" {
			return
		}
		opts.usageInstruction = text
	}
}

func applyOptions(configure ...Option) (options, error) {
	configured := options{
		instructionFormat:    InstructionFormatText,
		loadToolName:         defaultLoadToolName,
		readResourceToolName: defaultReadResourceToolName,
		maxResourceBytes:     defaultMaxResourceBytes,
	}
	for i, configureOption := range configure {
		if configureOption == nil {
			return options{}, fmt.Errorf("skill: options[%d] must not be nil", i)
		}
		configureOption(&configured)
	}
	if configured.instructionFormat != InstructionFormatText && configured.instructionFormat != InstructionFormatJSON {
		return options{}, fmt.Errorf("skill: unsupported instruction format %q", configured.instructionFormat)
	}
	if err := validateToolName("load tool", configured.loadToolName); err != nil {
		return options{}, err
	}
	if err := validateToolName("read resource tool", configured.readResourceToolName); err != nil {
		return options{}, err
	}
	if configured.maxResourceBytes <= 0 {
		return options{}, fmt.Errorf("skill: max resource bytes must be positive")
	}
	return configured, nil
}

func validateToolName(label, name string) error {
	if name == "" {
		return fmt.Errorf("skill: %s name must not be empty", label)
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("skill: %s name must be valid UTF-8", label)
	}
	return nil
}
