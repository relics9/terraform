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

// callClaude はプロンプトを送信してテキストレスポンスを返す共通関数
func callClaude(prompt string, maxTokens int64) string {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	log.Printf("[Claude IN] model=claude-opus-4-6 prompt=%d文字", len(prompt))

	message, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model("claude-opus-4-6")),
		MaxTokens: anthropic.F(maxTokens),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	if err != nil {
		log.Printf("[Claude IN] エラー: %v", err)
		return ""
	}
	log.Printf("[Claude OUT] usage=input:%d output:%d tokens", message.Usage.InputTokens, message.Usage.OutputTokens)
	return message.Content[0].Text
}

func analyzeWithClaude(errorContext string) map[string]interface{} {
	prompt := fmt.Sprintf(`あなたはGCPのエラーを分析するエキスパートエンジニアです。
以下のエラーログを分析して、JSON形式で回答してください。

エラーコンテキスト:
%s

以下のJSON形式で回答してください:
{
  "summary": "エラーの概要と原因の説明（Slack表示用、Markdownで）",
  "root_cause": "根本原因の特定",
  "severity": "critical/high/medium/low",
  "service_name": "エラーが発生したサービス名（例: example-api）。不明な場合は空文字",
  "should_create_pr": true/false,
  "pr_title": "PRタイトル（should_create_pr=trueの場合）",
  "pr_description": "PR説明（should_create_pr=trueの場合）",
  "fix_suggestion": {
    "file_path": "修正するファイルパス",
    "description": "修正内容の説明",
    "code_snippet": "修正コードの例"
  }
}

注意:
- should_create_pr はコードの修正が明確に特定できる場合のみ true にしてください
- 設定やインフラの問題はコメントのみで PR は作成しないでください`, errorContext)

	responseText := callClaude(prompt, 2000)
	if responseText == "" {
		return map[string]interface{}{
			"summary":          "Claude API呼び出しエラー",
			"should_create_pr": false,
		}
	}

	log.Printf("[Claude OUT] response=%s", truncate(responseText, 200))

	// JSONを抽出
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
		log.Printf("JSON解析エラー: %v", err)
		return map[string]interface{}{
			"summary":          responseText,
			"should_create_pr": false,
		}
	}
	return result
}
