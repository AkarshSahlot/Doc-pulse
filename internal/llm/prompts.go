package llm

import "fmt"

// ClassifierSystemPrompt tells the model how to decide if docs need updating.
const ClassifierSystemPrompt = `You are a pragmatic staff engineer doing documentation triage.

Rules:
- Answer with "YES" or "NO" followed by a short one-line reason.
- Internal refactors, formatting, and private-only changes do NOT need a doc update.
- Changes to exported APIs, config, CLI flags, or user-facing behavior DO need a doc update.
- When genuinely unsure, answer "YES" — a stale doc is worse than an unnecessary review.`

// ClassifierUserPrompt builds the user prompt for the classifier.
func ClassifierUserPrompt(codeContext string) string {
	return fmt.Sprintf(`Here is the code context for a merged pull request:

%s

Based on this diff, does any documentation need to be updated? Answer YES or NO with a brief reason.`, codeContext)
}

// UpdaterSystemPrompt tells the model how to regenerate documentation.
const UpdaterSystemPrompt = `You are a precise technical writer maintaining existing documentation.

Rules:
- Update only the sections that are outdated or missing given the code.
- Never invent features or behavior that isn't in the code.
- Keep sections that are still accurate untouched.
- Preserve the existing structure, headings, and tone.
- Briefly explain what changed, then output the full updated document.
- Output ONLY the final document, wrapped in <updated_doc> tags.`

// UpdaterUserPrompt builds the user prompt for the documentation updater.
func UpdaterUserPrompt(codeContext, docContent string) string {
	docSection := fmt.Sprintf("Current documentation:\n\n```\n%s\n```", docContent)
	instruction := "Compare the code with the documentation."
	if docContent == "" {
		docSection = "There is no existing documentation for this file yet."
		instruction = "Draft documentation from scratch based on the code."
	}

	return fmt.Sprintf(`Code context from the merged pull request:

%s

%s

Instructions:
1. %s
2. List any discrepancies (missing params, renamed exports, removed features, etc.).
3. Output the fully updated document wrapped in <updated_doc> and </updated_doc> tags.

If the documentation is already accurate, return it unchanged inside the tags.`,
		codeContext, docSection, instruction)
}
