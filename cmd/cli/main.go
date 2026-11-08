package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AkarshSahlot/docpulse/internal/config"
	"github.com/AkarshSahlot/docpulse/internal/llm"
	"github.com/AkarshSahlot/docpulse/internal/mapping"
	"github.com/spf13/cobra"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "docpulse",
	Short: "DocPulse keeps your documentation in sync with your code",
	Long: `docpulse reads a mapping file describing which docs track which code paths,
then uses an LLM to detect when a changed file makes a doc stale and regenerate it.

Unlike a fixed "README + changed file" heuristic, DocPulse lets you declare your
own code-to-doc rules in docpulse.yaml so it fits how your repo is actually organized.`,
}

var (
	codeFile    string
	docFile     string
	outputFile  string
	writeBack   bool
	skipCheck   bool
	mappingFile string
	dryRun      bool
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Analyze a code file and update its mapped documentation",
	Long: `sync reads a code file and a documentation file, asks an LLM whether the
docs are stale, and regenerates them if so.

Examples:
  docpulse sync --code=main.go --doc=README.md
  docpulse sync --code=internal/api/handler.go --doc=docs/api.md --write
  docpulse sync --code=main.go --doc=README.md --dry-run`,
	RunE: runSync,
}

var mapCmd = &cobra.Command{
	Use:   "map",
	Short: "Show which documentation files a code path maps to",
	Long: `map evaluates docpulse.yaml against a given code path and prints every
doc file whose rule matches, along with the reason it matched.

Example:
  docpulse map --code=internal/vcs/webhook.go`,
	RunE: runMap,
}

func init() {
	syncCmd.Flags().StringVar(&codeFile, "code", "", "path to the code file to analyze (required)")
	syncCmd.Flags().StringVar(&docFile, "doc", "", "path to the documentation file to update (required)")
	syncCmd.Flags().StringVar(&outputFile, "output", "", "path to write the updated documentation (default: stdout)")
	syncCmd.Flags().BoolVar(&writeBack, "write", false, "overwrite the original doc file in-place")
	syncCmd.Flags().BoolVar(&skipCheck, "skip-check", false, "skip the classification step and always regenerate")
	syncCmd.Flags().BoolVar(&dryRun, "dry-run", false, "classify and report, but never write or output the regenerated doc")
	_ = syncCmd.MarkFlagRequired("code")
	_ = syncCmd.MarkFlagRequired("doc")

	mapCmd.Flags().StringVar(&codeFile, "code", "", "path to the code file (required)")
	mapCmd.Flags().StringVar(&mappingFile, "mapping", "docpulse.yaml", "path to the mapping config")
	_ = mapCmd.MarkFlagRequired("code")

	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(mapCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	cfg, err := config.MustLoadForCLI()
	if err != nil {
		return err
	}

	codeContent, err := os.ReadFile(codeFile)
	if err != nil {
		return fmt.Errorf("failed to read code file %s: %w", codeFile, err)
	}

	var docContent []byte
	docExists := true
	docContent, err = os.ReadFile(docFile)
	if err != nil {
		if os.IsNotExist(err) {
			docExists = false
			fmt.Fprintf(os.Stderr, "doc file %s does not exist, will draft new documentation\n", docFile)
		} else {
			return fmt.Errorf("failed to read doc file %s: %w", docFile, err)
		}
	}

	llmClient, err := llm.NewClient(ctx, cfg.AnthropicAPIKey, cfg.LLMModel)
	if err != nil {
		return fmt.Errorf("failed to create LLM client: %w", err)
	}

	if !skipCheck && docExists {
		fmt.Fprintf(os.Stderr, "checking whether %s needs updating...\n", docFile)
		needsUpdate, reason, err := llmClient.ClassifyChange(ctx, string(codeContent))
		if err != nil {
			return fmt.Errorf("classification failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "classification: %s\n", reason)

		if !needsUpdate {
			fmt.Fprintf(os.Stderr, "%s is already up to date, nothing to do\n", docFile)
			return nil
		}
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "dry-run: %s would be regenerated, skipping write\n", docFile)
		return nil
	}

	fmt.Fprintf(os.Stderr, "regenerating %s...\n", docFile)
	updatedDoc, err := llmClient.UpdateDocumentation(ctx, string(codeContent), string(docContent))
	if err != nil {
		return fmt.Errorf("documentation update failed: %w", err)
	}
	if !strings.HasSuffix(updatedDoc, "\n") {
		updatedDoc += "\n"
	}

	switch {
	case writeBack:
		if err := writeWithDirs(docFile, updatedDoc); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote updated documentation to %s\n", docFile)
	case outputFile != "":
		if err := writeWithDirs(outputFile, updatedDoc); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote updated documentation to %s\n", outputFile)
	default:
		fmt.Print(updatedDoc)
	}

	return nil
}

func runMap(cmd *cobra.Command, args []string) error {
	rules, err := mapping.Load(mappingFile)
	if err != nil {
		return err
	}

	matches := rules.DocsFor(codeFile)
	if len(matches) == 0 {
		fmt.Printf("no mapping rule in %s matches %s\n", mappingFile, codeFile)
		return nil
	}

	fmt.Printf("%s maps to %d doc(s):\n\n", codeFile, len(matches))
	for i, m := range matches {
		fmt.Printf("  %d. %s\n     reason: %s\n\n", i+1, m.DocPath, m.Reason)
	}

	return nil
}

func writeWithDirs(path, content string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}
