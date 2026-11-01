// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yinx0004/wuzuo/internal/config"
	"github.com/yinx0004/wuzuo/internal/llm"
)

// listModels asks an endpoint for its models. Replaced in tests.
var listModels = func(ctx context.Context, r *config.ResolvedLLM) ([]llm.ModelInfo, error) {
	p, err := newProvider(r)
	if err != nil {
		return nil, err
	}
	lister, ok := p.(llm.ModelLister)
	if !ok {
		return nil, fmt.Errorf("%s cannot list its models", r.Preset)
	}
	return lister.ListModels(ctx)
}

const fetchDefaultLimit = 20

// endpoint is one API to ask: a provider preset, or a config alias that has
// its own base_url (a local server or a gateway).
type endpoint struct {
	label    string
	keyEnv   string
	resolved *config.ResolvedLLM
	// aliases maps model IDs on this endpoint to the short names pointing at them.
	aliases map[string][]string
}

func newModelsFetchCmd(g *globalFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch the current model list from each provider whose key is set",
		Long: `Ask each provider whose API key is set (and each alias with its own
base_url) which models it currently offers, newest first. Models that short
names point to are marked, newer models in the same family are flagged, and a
short name whose model is missing from the list is reported.

Nothing is changed: to use a newer model, pass -m provider/model-id, or point
a short name at it under "models:" in the config.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.load()
			if err != nil {
				return err
			}
			eps := fetchEndpoints(cfg)
			out := cmd.OutOrStdout()
			if len(eps) == 0 {
				return errors.New(config.NoKeyHelp())
			}
			failed := 0
			for i, ep := range eps {
				if i > 0 {
					fmt.Fprintln(out)
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
				models, err := listModels(ctx, ep.resolved)
				cancel()
				if err != nil {
					failed++
					fmt.Fprintf(out, "%s\n  ✗ %v\n", ep.header(), err)
					continue
				}
				printEndpointModels(out, ep, models, all)
			}
			if failed == len(eps) {
				return errors.New("could not fetch models from any provider")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, fmt.Sprintf("show every model, not only the newest %d", fetchDefaultLimit))
	return cmd
}

func (ep endpoint) header() string {
	h := ep.label
	var details []string
	if ep.keyEnv != "" {
		details = append(details, ep.keyEnv)
	}
	if ep.resolved.BaseURL != "" {
		details = append(details, ep.resolved.BaseURL)
	}
	if len(details) > 0 {
		h += " (" + strings.Join(details, ", ") + ")"
	}
	return h
}

// fetchEndpoints lists the providers with a key, plus config aliases that use
// their own base_url, without duplicates.
func fetchEndpoints(cfg *config.Config) []endpoint {
	var eps []endpoint
	seen := map[string]bool{}
	add := func(label, name, keyEnv string) *endpoint {
		r, err := cfg.ResolveEndpoint(name, envLookup)
		if err != nil {
			return nil
		}
		k := r.Preset + "|" + r.BaseURL + "|" + keyEnv
		if seen[k] {
			for i := range eps {
				if eps[i].resolved.Preset+"|"+eps[i].resolved.BaseURL+"|"+eps[i].keyEnv == k {
					return &eps[i]
				}
			}
		}
		seen[k] = true
		eps = append(eps, endpoint{label: label, keyEnv: keyEnv, resolved: r, aliases: map[string][]string{}})
		return &eps[len(eps)-1]
	}

	for _, p := range config.Presets {
		keyEnv, set, _ := config.KeyEnvFor(p.Name, envLookup)
		if !set {
			continue
		}
		add(p.Name, p.Name, keyEnv)
	}
	// Short names: built-in and config aliases on the provider's own endpoint,
	// config aliases with a base_url on theirs.
	for _, ch := range cfg.Choices(envLookup) {
		if ch.Model == "" {
			continue
		}
		var ep *endpoint
		if ch.Source == "config" && cfg.Models[ch.Name].BaseURL != "" {
			if !ch.Available {
				continue
			}
			ep = add(fmt.Sprintf("%s, alias %q", ch.Provider, ch.Name), ch.Name, ch.KeyEnv)
		} else {
			for i := range eps {
				if eps[i].label == ch.Provider {
					ep = &eps[i]
				}
			}
		}
		if ep != nil {
			ep.aliases[ch.Model] = append(ep.aliases[ch.Model], ch.Name)
		}
	}
	return eps
}

var (
	versionSuffix  = regexp.MustCompile(`(-\d+)+$`)
	snapshotSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)
)

// family strips a trailing version, e.g. claude-opus-5-5 -> claude-opus.
func family(id string) string {
	if f := versionSuffix.ReplaceAllString(id, ""); f != "" {
		return f
	}
	return id
}

func printEndpointModels(out io.Writer, ep endpoint, models []llm.ModelInfo, all bool) {
	fmt.Fprintln(out, ep.header())
	byID := map[string]llm.ModelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}

	// For each short name, find newer models of the same family.
	newer := map[string][]string{} // candidate ID -> notes
	upgrade := map[string]string{} // candidate ID -> first short name it could replace
	var missing []string
	aliasModels := make([]string, 0, len(ep.aliases))
	for id := range ep.aliases {
		aliasModels = append(aliasModels, id)
	}
	sort.Strings(aliasModels)
	for _, id := range aliasModels {
		names := strings.Join(quoteAll(ep.aliases[id]), ", ")
		cur, ok := byID[id]
		if !ok {
			missing = append(missing, fmt.Sprintf("%s → %s is not in this list (retired, renamed, or not enabled for this key)", names, id))
			continue
		}
		if cur.Created.IsZero() {
			continue
		}
		for _, m := range models {
			if m.ID != id && !m.Created.IsZero() && m.Created.After(cur.Created) &&
				family(m.ID) == family(id) && !snapshotSuffix.MatchString(m.ID) {
				newer[m.ID] = append(newer[m.ID], fmt.Sprintf("newer than %s (%s)", names, id))
				if _, ok := upgrade[m.ID]; !ok {
					upgrade[m.ID] = ep.aliases[id][0]
				}
			}
		}
	}

	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	shown, hidden := 0, 0
	for _, m := range models {
		var notes []string
		if names := ep.aliases[m.ID]; len(names) > 0 {
			notes = append(notes, "← "+strings.Join(names, ", "))
		}
		notes = append(notes, newer[m.ID]...)
		if !all && shown >= fetchDefaultLimit && len(notes) == 0 {
			hidden++
			continue
		}
		released := "-"
		if !m.Created.IsZero() {
			released = m.Created.Format("2006-01-02")
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\n", m.ID, released, strings.Join(notes, "; "))
		shown++
	}
	_ = w.Flush()
	if len(models) == 0 {
		fmt.Fprintln(out, "  (no models returned)")
	}
	if hidden > 0 {
		fmt.Fprintf(out, "  … %d more (use --all)\n", hidden)
	}
	for _, m := range missing {
		fmt.Fprintf(out, "  ! %s\n", m)
	}
	if len(newer) > 0 {
		var ids []string
		for id := range newer {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		fmt.Fprintf(out, "  To try it:   wuzuo ask -m %s/%s \"...\"\n", ep.resolved.Preset, ids[0])
		fmt.Fprintf(out, "  To switch %q to it, add to the config:\n    models: { %s: { provider: %s, model: %s } }\n",
			upgrade[ids[0]], upgrade[ids[0]], ep.resolved.Preset, ids[0])
	}
}

func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = fmt.Sprintf("%q", x)
	}
	return out
}
