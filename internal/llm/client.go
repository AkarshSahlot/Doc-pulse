package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

// Client wraps the Google Gemini API for documentation operations.
type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewClient creates a new LLM client with the given API key and model.
func NewClient(ctx context.Context, apiKey, model string) (*Client, error) {
	return &Client{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

type geminiRequest struct {
	SystemInstruction geminiContent   `json:"system_instruction"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  geminiGenConfig `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) generate(ctx context.Context, systemPrompt, userPrompt string, temperature float64, maxTokens int) (string, error) {
	reqBody := geminiRequest{
		SystemInstruction: geminiContent{Parts: []geminiPart{{Text: systemPrompt}}},
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: userPrompt}}},
		},
		GenerationConfig: geminiGenConfig{
			Temperature:     temperature,
			MaxOutputTokens: maxTokens,
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to encode Gemini request: %w", err)
	}

	url := fmt.Sprintf(geminiEndpoint, c.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to build Gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Gemini request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Gemini response: %w", err)
	}

	var parsed geminiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse Gemini response: %w", err)
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("Gemini API error: %s", parsed.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Gemini API returned status %d: %s", resp.StatusCode, string(body))
	}

	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("Gemini response did not contain text content")
	}

	var sb strings.Builder
	for _, part := range parsed.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	return sb.String(), nil
}

// ClassifyChange asks the LLM whether the given code change warrants a documentation update.
func (c *Client) ClassifyChange(ctx context.Context, codeContext string) (bool, string, error) {
	answer, err := c.generate(ctx, ClassifierSystemPrompt, ClassifierUserPrompt(codeContext), 0.1, 256)
	if err != nil {
		return false, "", fmt.Errorf("LLM classification request failed: %w", err)
	}

	needsUpdate := strings.HasPrefix(strings.TrimSpace(strings.ToUpper(answer)), "YES")
	return needsUpdate, answer, nil
}

// UpdateDocumentation asks the LLM to regenerate documentation given the current code and docs.
func (c *Client) UpdateDocumentation(ctx context.Context, codeContext, docContent string) (string, error) {
	content, err := c.generate(ctx, UpdaterSystemPrompt, UpdaterUserPrompt(codeContext, docContent), 0.2, 4096)
	if err != nil {
		return "", fmt.Errorf("LLM update request failed: %w", err)
	}

	return extractUpdatedDoc(content), nil
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
