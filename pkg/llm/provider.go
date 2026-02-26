package llm

import (
	"fmt"
)

type ProviderType string

const (
	ProviderOpenAI    ProviderType = "openai"
	ProviderAnthropic ProviderType = "anthropic"
	ProviderOllama    ProviderType = "ollama"
)

var providers = make(map[ProviderType]func(Config) (Provider, error))

type Config interface{}

type BaseConfig struct {
	APIKey  string
	BaseURL string
	Timeout int
}

func RegisterProvider(t ProviderType, factory func(Config) (Provider, error)) {
	providers[t] = factory
}

func GetProvider(t ProviderType, cfg Config) (Provider, error) {
	factory, ok := providers[t]
	if !ok {
		return nil, fmt.Errorf("provider %s not found", t)
	}
	return factory(cfg)
}

func ListProviders() []ProviderType {
	result := make([]ProviderType, 0, len(providers))
	for t := range providers {
		result = append(result, t)
	}
	return result
}
