package llm

import (
	"context"
	"errors"
	"net/http"

	"charm.land/fantasy"
	"charm.land/fantasy/schema"
	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

type failure struct {
	message string
	cause   error
}

func (e *failure) Error() string { return e.message }
func (e *failure) Unwrap() error { return e.cause }

func ResponseError(err error) error {
	return &progress.Error{Code: "llm_response_invalid", Cause: &failure{
		message: "The model returned an incomplete or invalid suggestion response. Try again or choose another model.", cause: err}}
}

// RequestError classifies native Fantasy errors without exposing provider
// response bodies, which may echo credentials, metadata, or stack traces.
func RequestError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var parse *schema.ParseError
	var object *fantasy.NoObjectGeneratedError
	if errors.As(err, &parse) || errors.As(err, &object) {
		return ResponseError(err)
	}
	code, message := "llm_request_failed", "The model request failed. Check your provider settings and try again."
	var provider *fantasy.ProviderError
	if errors.As(err, &provider) {
		switch {
		case provider.AuthError || provider.StatusCode == http.StatusUnauthorized || provider.StatusCode == http.StatusForbidden:
			code, message = "llm_authentication_failed", "The LLM provider rejected your credentials. Check the configured API key."
		case provider.StatusCode == http.StatusTooManyRequests:
			code, message = "llm_rate_limited", "The LLM provider is limiting requests. Wait and try again."
		}
	}
	return &progress.Error{Code: code, Cause: &failure{message: message, cause: err}}
}
