package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/schema"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestProvidersAndConfiguration(t *testing.T) {
	for _, provider := range []string{"openai", "anthropic", "openrouter", "openai-compat"} {
		t.Run(provider, func(t *testing.T) {
			c := Config{Provider: provider, Model: "test-model", APIKey: "test-key"}
			if provider == "openai-compat" {
				c.BaseURL = "http://localhost:1234/v1"
			}
			model, err := NewModel(context.Background(), c)
			if err != nil || model.Model() != c.Model || model.Provider() != provider {
				t.Fatalf("model=%v err=%v", model, err)
			}
		})
	}
	for _, c := range []Config{
		{}, {Model: "model", Provider: "unknown"},
		{Provider: "openai", Model: "model"}, {Provider: "openai-compat", Model: "model"},
		{Provider: "openrouter", Model: "model", APIKey: "key", BaseURL: "https://example.com"},
		{Provider: "openai", Model: "model", APIKey: "key", BaseURL: "file:///tmp/key"},
		{Provider: "openai", Model: "model", APIKey: "key", BaseURL: "https://user:key@example.com"},
		{Provider: "openai", Model: "model", APIKey: "key", BaseURL: "https://example.com?key=secret"},
		{Provider: "openai", Model: "model", APIKey: "key", BaseURL: "https://example.com#secret"},
	} {
		if err := c.Validate(); progress.ErrorCode(err) != "llm_configuration_invalid" {
			t.Fatalf("accepted invalid config: %+v err=%v", c, err)
		}
	}
}

func TestEnvironmentAndProviderOverride(t *testing.T) {
	for _, key := range []string{"ENGINE_LLM_PROVIDER", "ENGINE_LLM_MODEL", "ENGINE_LLM_BASE_URL", "ENGINE_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY"} {
		t.Setenv(key, "")
	}
	if _, err := NewModel(context.Background(), FromEnv()); progress.ErrorCode(err) != "llm_configuration_invalid" {
		t.Fatal("missing model accepted")
	}
	t.Setenv("ENGINE_LLM_MODEL", " test-model ")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")
	c := FromEnv()
	c.Provider = "anthropic"
	if got := c.withCredentials(); got.APIKey != "anthropic-key" {
		t.Fatal("provider flag inherited the wrong credentials")
	}
	model, err := NewModel(context.Background(), c)
	if err != nil || model.Provider() != "anthropic" || model.Model() != "test-model" {
		t.Fatalf("model=%v err=%v", model, err)
	}
	t.Setenv("ENGINE_LLM_API_KEY", "override")
	if got := FromEnv().withCredentials(); got.APIKey != "override" {
		t.Fatal("explicit key override was lost")
	}
	t.Setenv("ENGINE_LLM_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	if _, err := NewModel(context.Background(), FromEnv()); progress.ErrorCode(err) != "llm_configuration_invalid" {
		t.Fatal("missing credentials accepted")
	}
}

func TestFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timed_out"},
		{&fantasy.ProviderError{StatusCode: http.StatusUnauthorized, Message: "private trace"}, "llm_authentication_failed"},
		{&fantasy.ProviderError{AuthError: true}, "llm_authentication_failed"},
		{&fantasy.ProviderError{StatusCode: http.StatusForbidden}, "llm_authentication_failed"},
		{&fantasy.ProviderError{StatusCode: http.StatusTooManyRequests}, "llm_rate_limited"},
		{&fantasy.ProviderError{StatusCode: http.StatusInternalServerError}, "llm_request_failed"},
		{&schema.ParseError{}, "llm_response_invalid"}, {&fantasy.NoObjectGeneratedError{}, "llm_response_invalid"},
		{errors.New("private trace"), "llm_request_failed"},
	} {
		t.Run(tc.code+fmt.Sprintf("%T", tc.err), func(t *testing.T) {
			err := RequestError(tc.err)
			if progress.ErrorCode(err) != tc.code || !errors.Is(err, tc.err) || err.Error() == "private trace" {
				t.Fatalf("err=%v code=%s", err, progress.ErrorCode(err))
			}
		})
	}
}

func TestCompatibleProviderDoesNotInheritOpenAIKey(t *testing.T) {
	t.Setenv("ENGINE_LLM_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "private-openai-key")
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	model, err := NewModel(t.Context(), Config{Provider: "openai-compat", Model: "test-model", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Generate(t.Context(), fantasy.Call{Prompt: fantasy.Prompt{fantasy.NewUserMessage("test")}}); err != nil {
		t.Fatal(err)
	}
	if auth := <-received; strings.Contains(auth, "private-openai-key") {
		t.Fatal("compatible endpoint inherited unrelated provider credentials")
	}
}
