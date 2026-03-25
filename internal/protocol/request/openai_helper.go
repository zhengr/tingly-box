package request

import (
	"strings"

	"github.com/openai/openai-go/v3/packages/param"
)

func ParamOpt[T comparable](value T) param.Opt[T] {
	return param.NewOpt(value)
}

// RequiresMaxCompletionTokens checks if the model requires max_completion_tokens instead of max_tokens.
// Newer OpenAI models (gpt-4o, o1 series, gpt-4.1) use max_completion_tokens.
func RequiresMaxCompletionTokens(model string) bool {
	// Models that require max_completion_tokens
	modelsRequiringMaxCompletionTokens := []string{
		"gpt-4o",
		"gpt-4o-",
		"gpt-4o-mini",
		"gpt-4o-mini-",
		"o1-",
		"o1-2024",
		"chatgpt-4o",
		"chatgpt-4o-",
		"chatgpt-4o-mini",
		"chatgpt-4o-mini-",
		"gpt-4.1",
		"gpt-4.1-",
	}

	for _, prefix := range modelsRequiringMaxCompletionTokens {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}
