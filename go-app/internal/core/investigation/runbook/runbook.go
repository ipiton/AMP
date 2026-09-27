// Package runbook implements the PHASE-6B runbook engine: parsing markdown
// runbooks with YAML frontmatter, matching them against alert labels and
// rendering the matched set for the investigation prompt. It performs no I/O;
// loading from disk lives in infrastructure/investigation/runbooks.
package runbook

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxFileSize is the largest runbook file accepted by Parse (64 KiB).
const MaxFileSize = 64 * 1024

const frontmatterDelim = "---"

// Runbook is one operator-provided investigation guide.
type Runbook struct {
	Name  string
	Match map[string]string
	Tags  []string
	Body  string
	// Source identifies the origin (relative file path); used for logs and
	// as the final ordering tie-breaker.
	Source string
}

type frontmatter struct {
	Name  string            `yaml:"name"`
	Match map[string]string `yaml:"match"`
	Tags  []string          `yaml:"tags"`
}

// Parse parses a runbook file: a leading "---" line, YAML frontmatter, a
// closing "---" line, then the markdown body. Unknown frontmatter fields are
// ignored.
func Parse(source string, data []byte) (Runbook, error) {
	if len(data) > MaxFileSize {
		return Runbook{}, fmt.Errorf("runbook %s: file size %d exceeds %d bytes", source, len(data), MaxFileSize)
	}

	text := strings.ReplaceAll(string(bytes.TrimPrefix(data, []byte("\uFEFF"))), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelim {
		return Runbook{}, fmt.Errorf("runbook %s: missing frontmatter (first line must be %q)", source, frontmatterDelim)
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterDelim {
			end = i
			break
		}
	}
	if end < 0 {
		return Runbook{}, fmt.Errorf("runbook %s: unterminated frontmatter (missing closing %q)", source, frontmatterDelim)
	}

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return Runbook{}, fmt.Errorf("runbook %s: invalid frontmatter: %w", source, err)
	}
	if err := validate(fm); err != nil {
		return Runbook{}, fmt.Errorf("runbook %s: %w", source, err)
	}

	return Runbook{
		Name:   strings.TrimSpace(fm.Name),
		Match:  fm.Match,
		Tags:   fm.Tags,
		Body:   strings.TrimSpace(strings.Join(lines[end+1:], "\n")),
		Source: source,
	}, nil
}

func validate(fm frontmatter) error {
	if strings.TrimSpace(fm.Name) == "" {
		return errors.New("frontmatter: name is required")
	}
	if len(fm.Match) == 0 {
		return errors.New("frontmatter: match must contain at least one label")
	}
	for k, v := range fm.Match {
		if strings.TrimSpace(k) == "" {
			return errors.New("frontmatter: match contains an empty label name")
		}
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("frontmatter: match label %q has an empty value", k)
		}
	}
	return nil
}
