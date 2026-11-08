package mapping

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rule links a glob pattern over changed code paths to the doc files that should be re-checked.
type Rule struct {
	Code   string   `yaml:"code"`
	Docs   []string `yaml:"docs"`
	Reason string   `yaml:"reason"`

	compiled *regexp.Regexp
}

// Config is the top-level docpulse.yaml structure.
type Config struct {
	Rules []Rule `yaml:"mappings"`
}

// Load reads and compiles a mapping config from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read mapping file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse mapping file %s: %w", path, err)
	}

	for i := range cfg.Rules {
		re, err := compileGlob(cfg.Rules[i].Code)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q in mapping file %s: %w", cfg.Rules[i].Code, path, err)
		}
		cfg.Rules[i].compiled = re
	}

	return &cfg, nil
}

// Match describes a doc file that should be reviewed because of a matching code change.
type Match struct {
	DocPath string
	Reason  string
}

// DocsFor returns, deduplicated, every doc that a changed code path is mapped to.
func (c *Config) DocsFor(changedPath string) []Match {
	seen := make(map[string]bool)
	var matches []Match

	for _, rule := range c.Rules {
		if rule.compiled == nil || !rule.compiled.MatchString(changedPath) {
			continue
		}
		for _, doc := range rule.Docs {
			if seen[doc] {
				continue
			}
			seen[doc] = true
			matches = append(matches, Match{DocPath: doc, Reason: rule.Reason})
		}
	}

	return matches
}

// DocsForAll merges the doc matches for a batch of changed paths.
func (c *Config) DocsForAll(changedPaths []string) []Match {
	seen := make(map[string]bool)
	var matches []Match

	for _, path := range changedPaths {
		for _, m := range c.DocsFor(path) {
			if seen[m.DocPath] {
				continue
			}
			seen[m.DocPath] = true
			matches = append(matches, m)
		}
	}

	return matches
}

// compileGlob turns a "**"/"*" code-path glob into a regexp.
// "**" matches any run of characters, including "/"; "*" matches any run
// of characters within a single path segment.
func compileGlob(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.WriteString("^")

	for i := 0; i < len(pattern); {
		switch {
		case strings.HasPrefix(pattern[i:], "**"):
			sb.WriteString(".*")
			i += 2
		case pattern[i] == '*':
			sb.WriteString("[^/]*")
			i++
		default:
			sb.WriteString(regexp.QuoteMeta(string(pattern[i])))
			i++
		}
	}

	sb.WriteString("$")
	return regexp.Compile(sb.String())
}
