package vcs

import (
	"context"
	"fmt"
	"log"

	"github.com/AkarshSahlot/docpulse/internal/llm"
	"github.com/AkarshSahlot/docpulse/internal/mapping"
	"github.com/AkarshSahlot/docpulse/internal/store"
)

// WebhookHandler processes merged-PR events and keeps mapped docs in sync.
type WebhookHandler struct {
	llmClient *llm.Client
	rules     *mapping.Config
	dryRun    bool
	events    *store.Store
}

// NewWebhookHandler creates a new webhook event handler.
// When dryRun is true, matching docs are classified and a diff is logged but no PR is opened.
// events may be nil, in which case sync outcomes are simply not recorded.
func NewWebhookHandler(llmClient *llm.Client, rules *mapping.Config, dryRun bool, events *store.Store) *WebhookHandler {
	return &WebhookHandler{
		llmClient: llmClient,
		rules:     rules,
		dryRun:    dryRun,
		events:    events,
	}
}

// HandleMergedPR processes a merged pull request event: it fetches the diff,
// resolves which docs the mapping config says are affected, and updates them.
func (h *WebhookHandler) HandleMergedPR(ctx context.Context, client *Client, repoFullName string, prNumber int, baseBranch string) error {
	log.Printf("[docpulse] processing merged PR #%d", prNumber)

	diff, err := client.GetPRDiff(ctx, prNumber)
	if err != nil {
		return fmt.Errorf("failed to get PR diff: %w", err)
	}
	log.Printf("[docpulse] PR #%d touched %d files", prNumber, len(diff.Files))

	changedPaths := make([]string, 0, len(diff.Files))
	for _, f := range diff.Files {
		changedPaths = append(changedPaths, f.Filename)
	}

	matches := h.rules.DocsForAll(changedPaths)
	if len(matches) == 0 {
		log.Printf("[docpulse] no mapping rule matched any file in PR #%d, skipping", prNumber)
		return nil
	}
	log.Printf("[docpulse] PR #%d maps to %d candidate doc(s)", prNumber, len(matches))

	var codeContext string
	for _, f := range diff.Files {
		codeContext += fmt.Sprintf("--- File: %s ---\n%s\n\n", f.Filename, f.Patch)
	}

	for _, match := range matches {
		if err := h.syncDoc(ctx, client, repoFullName, prNumber, baseBranch, match, codeContext); err != nil {
			log.Printf("[docpulse] error syncing %s for PR #%d: %v", match.DocPath, prNumber, err)
			// keep going for the remaining docs
		}
	}

	return nil
}

func (h *WebhookHandler) recordEvent(e store.Event) {
	if h.events == nil {
		return
	}
	h.events.Add(e)
}

func (h *WebhookHandler) syncDoc(ctx context.Context, client *Client, repoFullName string, prNumber int, baseBranch string, match mapping.Match, codeContext string) error {
	log.Printf("[docpulse] checking %s (matched: %s)", match.DocPath, match.Reason)

	evt := store.Event{
		Source:   "webhook",
		Repo:     repoFullName,
		PRNumber: prNumber,
		DocPath:  match.DocPath,
		DryRun:   h.dryRun,
	}

	docContent, err := client.GetFileContent(ctx, match.DocPath, baseBranch)
	if err != nil {
		evt.Status = "error"
		evt.Error = err.Error()
		h.recordEvent(evt)
		return fmt.Errorf("failed to get doc content: %w", err)
	}

	needsUpdate, reason, err := h.llmClient.ClassifyChange(ctx, codeContext)
	if err != nil {
		evt.Status = "error"
		evt.Error = err.Error()
		h.recordEvent(evt)
		return fmt.Errorf("classification failed: %w", err)
	}
	evt.NeedsUpdate = needsUpdate
	evt.Reason = reason
	log.Printf("[docpulse] classification for %s: %s", match.DocPath, reason)

	if !needsUpdate {
		evt.Status = "skipped"
		h.recordEvent(evt)
		log.Printf("[docpulse] %s does not need updating", match.DocPath)
		return nil
	}

	updatedDoc, err := h.llmClient.UpdateDocumentation(ctx, codeContext, docContent)
	if err != nil {
		evt.Status = "error"
		evt.Error = err.Error()
		h.recordEvent(evt)
		return fmt.Errorf("doc update generation failed: %w", err)
	}

	if h.dryRun {
		evt.Status = "dry_run"
		evt.UpdatedDoc = updatedDoc
		h.recordEvent(evt)
		log.Printf("[docpulse] dry-run: would update %s for PR #%d (%d bytes)", match.DocPath, prNumber, len(updatedDoc))
		return nil
	}

	pr, err := client.CreateDocUpdatePR(ctx, baseBranch, match.DocPath, updatedDoc, prNumber)
	if err != nil {
		evt.Status = "error"
		evt.Error = err.Error()
		h.recordEvent(evt)
		return fmt.Errorf("failed to create doc update PR: %w", err)
	}

	evt.Status = "pr_created"
	evt.PRURL = pr.GetHTMLURL()
	h.recordEvent(evt)

	log.Printf("[docpulse] opened PR #%d updating %s", pr.GetNumber(), match.DocPath)
	return nil
}
