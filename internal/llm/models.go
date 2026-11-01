// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
)

// ModelInfo describes a model a provider offers to the configured key.
type ModelInfo struct {
	ID      string
	Name    string
	Created time.Time // zero when the provider does not report it
}

// ModelLister is implemented by providers that can list their models.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// ErrModelNotFound means the provider does not offer the requested model to
// this key: it was retired, renamed, or is not enabled for the account.
var ErrModelNotFound = errors.New("model not available")

func notFound(model, provider, baseURL string) error {
	where := provider
	if baseURL != "" {
		where += " at " + baseURL
	}
	return fmt.Errorf("%w: %q is not offered by %s (retired, renamed, or not enabled for this key). Run `wuzuo models fetch` to see the current models", ErrModelNotFound, model, where)
}

// ListModels implements ModelLister.
func (a *Anthropic) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var out []ModelInfo
	pager := a.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	for pager.Next() {
		m := pager.Current()
		out = append(out, ModelInfo{ID: m.ID, Name: m.DisplayName, Created: m.CreatedAt})
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("%s: list models: %w", a.opts.Name, err)
	}
	sortModels(out)
	return out, nil
}

// ListModels implements ModelLister.
func (p *OpenAI) ListModels(ctx context.Context) ([]ModelInfo, error) {
	var out []ModelInfo
	pager := p.client.Models.ListAutoPaging(ctx)
	for pager.Next() {
		m := pager.Current()
		info := ModelInfo{ID: m.ID}
		if m.Created > 0 {
			info.Created = time.Unix(m.Created, 0).UTC()
		}
		out = append(out, info)
	}
	if err := pager.Err(); err != nil {
		return nil, fmt.Errorf("%s: list models: %w", p.opts.Name, err)
	}
	sortModels(out)
	return out, nil
}

// sortModels orders newest first; models without a date go last, by ID.
func sortModels(ms []ModelInfo) {
	sort.SliceStable(ms, func(i, j int) bool {
		a, b := ms[i].Created, ms[j].Created
		if a.IsZero() != b.IsZero() {
			return !a.IsZero()
		}
		if !a.Equal(b) {
			return a.After(b)
		}
		return ms[i].ID < ms[j].ID
	})
}

func isNotFound(err error) bool {
	var ae *anthropic.Error
	if errors.As(err, &ae) {
		return ae.StatusCode == 404
	}
	var oe *openai.Error
	if errors.As(err, &oe) {
		return oe.StatusCode == 404
	}
	return false
}
