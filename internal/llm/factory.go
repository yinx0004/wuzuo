// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"fmt"
	"strings"

	"github.com/yinx0004/wuzuo/internal/config"
)

// New builds a Provider from a resolved configuration.
func New(r *config.ResolvedLLM) (Provider, error) {
	switch r.Kind {
	case config.KindAnthropic:
		return NewAnthropic(AnthropicOptions{
			Name:    r.Preset,
			Model:   r.Model,
			APIKey:  r.APIKey,
			BaseURL: r.BaseURL,
			Effort:  r.Effort,
			// Server-side fallback is a first-party API feature; a gateway may
			// not understand the field.
			RefusalFallback: r.RefusalFallback && (r.BaseURL == "" || strings.Contains(r.BaseURL, "api.anthropic.com")),
			ThinkingSummary: r.ThinkingSummary,
		}), nil
	case config.KindOpenAI:
		return NewOpenAI(OpenAIOptions{
			Name:    r.Preset,
			Model:   r.Model,
			APIKey:  r.APIKey,
			BaseURL: r.BaseURL,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", r.Kind)
	}
}
