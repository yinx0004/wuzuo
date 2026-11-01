// SPDX-License-Identifier: Apache-2.0

// Package toolset defines the contract between WuZuo's investigation loop and
// the data sources it can examine. A Toolset groups related read-only tools
// (for example everything that inspects MySQL); each Tool is exposed to the
// LLM as a function it can call.
//
// Implementations must never mutate the systems they inspect.
package toolset

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Spec describes a tool to the LLM.
type Spec struct {
	// Name must match ^[a-zA-Z0-9_-]{1,64}$. Prefix it with the toolset name,
	// e.g. "mysql_lock_waits".
	Name string
	// Description tells the model when the tool is useful and what it returns.
	Description string
	// Parameters is a JSON Schema object describing the arguments.
	Parameters Schema
}

// Schema is a JSON Schema object for tool arguments.
type Schema struct {
	Properties map[string]any
	Required   []string
}

// Tool is a single read-only probe.
type Tool interface {
	Spec() Spec
	// Run executes the probe. The returned text is what the investigator sees
	// and what is sealed into the evidence locker, so it should be compact and
	// self-explanatory (column headers, units, sampling windows).
	Run(ctx context.Context, args json.RawMessage) (*Result, error)
}

// Result is the output of a tool run.
type Result struct {
	// Target is the configured target the tool examined, if any.
	Target string
	// Content is the textual output.
	Content string
}

// CheckResult reports one prerequisite check, e.g. "can connect" or
// "has PROCESS privilege".
type CheckResult struct {
	Target string
	Item   string
	OK     bool
	Detail string
}

// Toolset is a group of tools sharing configuration and connections.
type Toolset interface {
	Name() string
	Description() string
	// Check verifies prerequisites (connectivity, privileges, server features).
	Check(ctx context.Context) []CheckResult
	Tools() []Tool
	Close() error
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidateSpec returns an error if the spec cannot be sent to an LLM provider.
func ValidateSpec(s Spec) error {
	if !validName.MatchString(s.Name) {
		return fmt.Errorf("invalid tool name %q: must match %s", s.Name, validName)
	}
	if strings.TrimSpace(s.Description) == "" {
		return fmt.Errorf("tool %q has no description", s.Name)
	}
	return nil
}

// DecodeArgs unmarshals tool arguments, treating empty input as an empty object.
func DecodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}
