package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// Client wraps the Anthropic Claude API for documentation operations.
type Client struct {
	api   anthropic.Client
	model anthropic.Model
}

// NewClient creates a new LLM client with the given API key and model.
func NewClient(ctx context.Context, apiKey, model string) (*Client, error) {
	return &Client{
		api:   anthropic.NewClient(option.WithAPIKey(apiKey)),
		model: anthropic.Model(model),
	}, nil
}

// ClassifyChange asks the LLM whether the given code change warrants a documentation update.
func (c *Client) ClassifyChange(ctx context.Context, codeContext string) (bool, string, error) {
	params := anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 256,
		System: []anthropic.TextBlockParam{
			{Text: ClassifierSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(ClassifierUserPrompt(codeContext))),
		},
		Temperature: param.Opt[float64]{Value: 0.1},
	}

	resp, err := c.api.Messages.New(ctx, params)
	if err != nil {
		return false, "", fmt.Errorf("LLM classification request failed: %w", err)
	}

	answer := firstText(resp)
	if answer == "" {
		return false, "", fmt.Errorf("LLM response did not contain text content")
	}

	needsUpdate := strings.HasPrefix(strings.TrimSpace(strings.ToUpper(answer)), "YES")
	return needsUpdate, answer, nil
}

// UpdateDocumentation asks the LLM to regenerate documentation given the current code and docs.
func (c *Client) UpdateDocumentation(ctx context.Context, codeContext, docContent string) (string, error) {
	params := anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: 4096,
		System: []anthropic.TextBlockParam{
			{Text: UpdaterSystemPrompt},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(UpdaterUserPrompt(codeContext, docContent))),
		},
		Temperature: param.Opt[float64]{Value: 0.2},
	}

	resp, err := c.api.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("LLM update request failed: %w", err)
	}

	content := allText(resp)
	if content == "" {
		return "", fmt.Errorf("LLM returned no content")
	}

	return extractUpdatedDoc(content), nil
}

func firstText(resp *anthropic.Message) string {
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			return block.Text
		}
	}
	return ""
}

func allText(resp *anthropic.Message) string {
	var sb strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String()
}

// extractUpdatedDoc pulls the content out of <updated_doc> tags, falling back
// to the whole response when the model didn't wrap its answer.
func extractUpdatedDoc(content string) string {
	const openTag = "<updated_doc>"
	const closeTag = "</updated_doc>"

	start := strings.Index(content, openTag)
	if start == -1 {
		return strings.TrimSpace(content)
	}
	start += len(openTag)

	end := strings.Index(content[start:], closeTag)
	if end == -1 {
		return strings.TrimSpace(content[start:])
	}

	return strings.TrimSpace(content[start : start+end])
}
