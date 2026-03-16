package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// callClaude sends a prompt and returns the text response.
func callClaude(prompt string, maxTokens int64) string {
	model := os.Getenv("ANTHROPIC_CLAUDE_MODEL")
	if model == "" {
		model = "claude-opus-4-6"
	}
	return callClaudeWithModel(prompt, maxTokens, model)
}


func callClaudeWithModel(prompt string, maxTokens int64, model string) string {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	log.Printf("[Claude IN] model=%s prompt=%d chars", model, len(prompt))

	message, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(model)),
		MaxTokens: anthropic.F(maxTokens),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	if err != nil {
		log.Printf("[Claude IN] error: %v", err)
		return ""
	}
	log.Printf("[Claude OUT] model=%s usage=input:%d output:%d tokens", model, message.Usage.InputTokens, message.Usage.OutputTokens)
	return message.Content[0].Text
}

func analyzeWithClaude(errorContext string) map[string]interface{} {
	prompt := fmt.Sprintf(`You are an expert engineer specializing in GCP error analysis.
Analyze the following error log and respond in JSON format.

Error context:
%s

Respond in the following JSON format:
{
  "summary": "Summary and explanation of the error (for Slack display, in Markdown)",
  "root_cause": "Identified root cause",
  "severity": "critical/high/medium/low",
  "service_name": "Name of the service where the error occurred (e.g. example-api). Empty string if unknown.",
  "should_create_pr": true/false,
  "pr_title": "PR title (if should_create_pr=true)",
  "pr_description": "PR description (if should_create_pr=true)",
  "fix_suggestion": {
    "file_path": "Path of the file to fix",
    "description": "Description of the fix",
    "code_snippet": "Example fix code"
  }
}

Notes:
- Set should_create_pr to true only when a specific code fix can be clearly identified
- For configuration or infrastructure issues, do not create a PR`, errorContext)

	responseText := callClaude(prompt, 2000)
	if responseText == "" {
		return map[string]interface{}{
			"summary":          "Claude API call error",
			"should_create_pr": false,
		}
	}

	log.Printf("[Claude OUT] response=%s", truncate(responseText, 200))

	// Extract JSON from response
	if strings.Contains(responseText, "```json") {
		parts := strings.SplitN(responseText, "```json", 2)
		responseText = strings.SplitN(parts[1], "```", 2)[0]
	} else if strings.Contains(responseText, "```") {
		parts := strings.SplitN(responseText, "```", 2)
		responseText = strings.SplitN(parts[1], "```", 2)[0]
	} else {
		start := strings.Index(responseText, "{")
		end := strings.LastIndex(responseText, "}")
		if start != -1 && end > start {
			responseText = responseText[start : end+1]
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(responseText)), &result); err != nil {
		log.Printf("JSON parse error: %v", err)
		return map[string]interface{}{
			"summary":          responseText,
			"should_create_pr": false,
		}
	}
	return result
}
