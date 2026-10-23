// Package llm configures Fantasy providers shared by CLI and HTTP operations.
package llm

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"charm.land/fantasy/providers/openrouter"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
	"github.com/charmbracelet/openai-go/option"
)

type Config struct {
	Provider string
	Model    string
	BaseURL  string
	APIKey   string
}

// FromEnv reads initial model settings at the application boundary. Provider
// credentials resolve after flag overrides; HTTP requests cannot set them.
func FromEnv() Config {
	c := Config{Provider: strings.TrimSpace(os.Getenv("ENGINE_LLM_PROVIDER")),
		Model:   strings.TrimSpace(os.Getenv("ENGINE_LLM_MODEL")),
		BaseURL: strings.TrimSpace(os.Getenv("ENGINE_LLM_BASE_URL")), APIKey: os.Getenv("ENGINE_LLM_API_KEY")}
	if c.Provider == "" {
		c.Provider = "openai"
	}
	return c
}

func (c Config) withCredentials() Config {
	if c.APIKey == "" {
		c.APIKey = os.Getenv("ENGINE_LLM_API_KEY")
	}
	if c.APIKey == "" {
		keys := map[string]string{"openai": "OPENAI_API_KEY", "anthropic": "ANTHROPIC_API_KEY", "openrouter": "OPENROUTER_API_KEY"}
		c.APIKey = os.Getenv(keys[c.Provider])
	}
	return c
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Model) == "" {
		return configError("Choose a model with --llm-model or ENGINE_LLM_MODEL.")
	}
	switch c.Provider {
	case "openai", "anthropic", "openrouter":
		if strings.TrimSpace(c.APIKey) == "" {
			return configError("Set ENGINE_LLM_API_KEY or the selected provider's API key environment variable.")
		}
	case "openai-compat":
		if c.BaseURL == "" {
			return configError("Set --llm-base-url or ENGINE_LLM_BASE_URL for the compatible provider.")
		}
	default:
		return configError("Choose an LLM provider: openai, anthropic, openrouter, or openai-compat.")
	}
	if c.BaseURL != "" {
		if c.Provider == "openrouter" {
			return configError("OpenRouter uses its standard service URL. Use openai-compat for a custom endpoint.")
		}
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return configError("Use an http(s) LLM base URL without credentials, query parameters, or fragments.")
		}
	}
	return nil
}

func configError(message string) error {
	return &progress.Error{Code: "llm_configuration_invalid", Cause: errors.New(message)}
}

// NewModel uses Fantasy's provider implementations and object modes directly.
func NewModel(ctx context.Context, c Config) (fantasy.LanguageModel, error) {
	c = c.withCredentials()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var provider fantasy.Provider
	var err error
	switch c.Provider {
	case "anthropic":
		provider, err = anthropic.New(anthropic.WithAPIKey(c.APIKey), anthropic.WithBaseURL(c.BaseURL))
	case "openrouter":
		provider, err = openrouter.New(openrouter.WithAPIKey(c.APIKey))
	case "openai-compat":
		// Explicit SDK credentials prevent an unrelated OPENAI_API_KEY from
		// being inherited by a local or separately authenticated endpoint.
		provider, err = openaicompat.New(openaicompat.WithBaseURL(c.BaseURL),
			openaicompat.WithSDKOptions(option.WithAPIKey(c.APIKey)))
	default:
		provider, err = openai.New(openai.WithAPIKey(c.APIKey), openai.WithBaseURL(c.BaseURL))
	}
	if err != nil {
		return nil, configError("Could not configure the selected LLM provider.")
	}
	return provider.LanguageModel(ctx, c.Model)
}
