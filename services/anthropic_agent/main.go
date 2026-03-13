// Cloud Run Service: Anthropic Agent
// 1. Pub/Sub Push: GCPエラーログをClaudeで分析してSlackに通知 (/notify)
// 2. Slack Events API: @メンションを受けてGitHub PR/Issueを作成 (/)
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/notify", handlePubSubNotify) // Pub/Sub Push サブスクリプション
	http.HandleFunc("/", handleSlackEvent)          // Slack Events API
	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

// ==============================================================================
// Pub/Sub Push: エラーログをClaudeで分析してSlackに通知
// ==============================================================================

type pubSubMessage struct {
	Message struct {
		Data      string `json:"data"`
		MessageID string `json:"messageId"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

func handlePubSubNotify(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	var msg pubSubMessage
	if err := json.Unmarshal(bodyBytes, &msg); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Pub/Sub メッセージをデコード
	decoded, err := base64.StdEncoding.DecodeString(msg.Message.Data)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// ログエントリを解析
	var logEntry map[string]interface{}
	if err := json.Unmarshal(decoded, &logEntry); err != nil {
		logEntry = map[string]interface{}{"textPayload": string(decoded)}
	}

	severity := getStrOr(logEntry, "severity", "ERROR")
	resource, _ := logEntry["resource"].(map[string]interface{})
	resourceType := "unknown"
	labels := map[string]interface{}{}
	if resource != nil {
		resourceType = getStrOr(resource, "type", "unknown")
		if l, ok := resource["labels"].(map[string]interface{}); ok {
			labels = l
		}
	}
	errorMessage := getStr(logEntry, "textPayload")
	if errorMessage == "" {
		if jp, ok := logEntry["jsonPayload"].(map[string]interface{}); ok {
			// log/slog は "msg"、他のロガーは "message" を使う
			errorMessage = getStr(jp, "msg")
			if errorMessage == "" {
				errorMessage = getStr(jp, "message")
			}
		}
	}
	if errorMessage == "" {
		// フォールバック: ログエントリ全体をJSON文字列としてClaudeに渡す
		if raw, err := json.Marshal(logEntry); err == nil {
			errorMessage = string(raw)
		} else {
			errorMessage = "詳細不明のエラー"
		}
	}

	// サービス名を特定
	serviceName := ""
	for _, key := range []string{"service_name", "function_name", "job_name"} {
		if v, ok := labels[key].(string); ok && v != "" {
			serviceName = v
			break
		}
	}

	// Cloud Logging の該当ログへの直接リンクを生成
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		projectID = "unknown"
	}
	logName := getStr(logEntry, "logName")
	insertID := getStr(logEntry, "insertId")
	timestamp := getStr(logEntry, "timestamp")
	loggingURL := buildLoggingURL(projectID, logName, insertID, timestamp)

	log.Printf("Pub/Sub受信: severity=%s, resource=%s, service=%s", severity, resourceType, serviceName)

	// Claudeでエラーを分析
	analysis := analyzeErrorForNotification(severity, resourceType, errorMessage, logEntry)

	// Slackに分析結果を通知
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")

	severityEmoji := map[string]string{
		"CRITICAL":  ":red_circle:",
		"ALERT":     ":red_circle:",
		"EMERGENCY": ":sos:",
		"ERROR":     ":warning:",
		"WARNING":   ":large_yellow_circle:",
	}
	emoji := severityEmoji[severity]
	if emoji == "" {
		emoji = ":warning:"
	}

	// サービス名フィールドを構築
	serviceField := fmt.Sprintf("*サービス名:*\n`%s`", serviceName)
	if serviceName == "" {
		serviceField = fmt.Sprintf("*リソース種別:*\n%s", resourceType)
	}

	slackMsg := map[string]interface{}{
		"blocks": []map[string]interface{}{
			{
				"type": "header",
				"text": map[string]string{
					"type": "plain_text",
					"text": fmt.Sprintf("%s GCPエラー検知: %s", emoji, severity),
				},
			},
			{
				"type": "section",
				"fields": []map[string]string{
					{"type": "mrkdwn", "text": serviceField},
					{"type": "mrkdwn", "text": fmt.Sprintf("*リソース種別:*\n%s", resourceType)},
					{"type": "mrkdwn", "text": fmt.Sprintf("*エラー:*\n```%s```", truncate(errorMessage, 200))},
				},
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": fmt.Sprintf(":brain: *AI分析:*\n%s", analysis),
				},
			},
			{
				"type": "actions",
				"elements": []map[string]interface{}{
					{
						"type":  "button",
						"text":  map[string]string{"type": "plain_text", "text": "Cloud Loggingで確認"},
						"url":   loggingURL,
						"style": "danger",
					},
				},
			},
			{"type": "divider"},
			{
				"type": "context",
				"elements": []map[string]string{
					{
						"type": "mrkdwn",
						"text": ":robot_face: このメッセージのスレッド内でメンションしてください\n• `@relics9-bot fix` - AI分析 & GitHub PRを自動作成\n• `@relics9-bot issue` - AI分析 & GitHub Issueを作成",
					},
				},
			},
		},
	}

	data, _ := json.Marshal(slackMsg)
	req, _ := http.NewRequest("POST", webhookURL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	if _, err := client.Do(req); err != nil {
		log.Printf("Slack通知エラー: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	log.Printf("Slack通知完了: severity=%s, resource=%s", severity, resourceType)
	w.WriteHeader(http.StatusOK)
}

func analyzeErrorForNotification(severity, resourceType, errorMessage string, logEntry map[string]interface{}) string {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))

	// GitHubからリポジトリのコードを取得してコンテキストに追加
	repoContext := fetchRepoContext(logEntry)

	prompt := fmt.Sprintf(`GCPエラーログを簡潔に分析してください。Markdownで回答してください。

重大度: %s
リソース: %s
エラー: %s

%s
- 何が起きたか
- 考えられる原因（コードの具体的な箇所があれば指摘してください）
- 推奨アクション`, severity, resourceType, errorMessage, repoContext)

	log.Printf("[Claude IN] analyzeErrorForNotification model=claude-opus-4-6 prompt=%d文字", len(prompt))
	message, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model("claude-opus-4-6")),
		MaxTokens: anthropic.F(int64(1000)),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	if err != nil {
		log.Printf("[Claude IN] エラー: %v", err)
		return fmt.Sprintf("エラーメッセージ: %s", truncate(errorMessage, 300))
	}
	log.Printf("[Claude OUT] usage=input:%d output:%d tokens", message.Usage.InputTokens, message.Usage.OutputTokens)
	return message.Content[0].Text
}

// fetchRepoContext はログエントリからサービス名を判定し、GitHubのコードを取得する
func fetchRepoContext(logEntry map[string]interface{}) string {
	owner := os.Getenv("GITHUB_OWNER")
	token := os.Getenv("GITHUB_TOKEN")
	if owner == "" || token == "" {
		return ""
	}

	// ログのリソースラベルからサービス名を特定
	resource, _ := logEntry["resource"].(map[string]interface{})
	labels, _ := resource["labels"].(map[string]interface{})
	serviceName := ""
	for _, key := range []string{"service_name", "function_name", "job_name"} {
		if v, ok := labels[key].(string); ok && v != "" {
			serviceName = v
			break
		}
	}
	if serviceName == "" {
		return ""
	}

	// サービス名と同名のリポジトリを探す
	repo := serviceName
	headers := githubHeaders(token)

	// リポジトリのファイルツリーを取得
	treeResp, err := githubRequest("GET",
		fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/HEAD?recursive=1", owner, repo),
		headers, nil)
	if err != nil {
		// サービス名の末尾サフィックスを除いて再試行 (例: example-api → example)
		for _, suffix := range []string{"-api", "-service", "-svc", "-app", "-worker"} {
			if strings.HasSuffix(repo, suffix) {
				trimmed := strings.TrimSuffix(repo, suffix)
				treeResp, err = githubRequest("GET",
					fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/HEAD?recursive=1", owner, trimmed),
					headers, nil)
				if err == nil {
					repo = trimmed
					break
				}
			}
		}
		if err != nil {
			return fmt.Sprintf("## GitHubリポジトリ\nリポジトリ `%s/%s` が見つかりませんでした。\n\n", owner, serviceName)
		}
	}

	// ソースファイルのパスを収集 (最大20件)
	tree, _ := treeResp["tree"].([]interface{})
	var sourceFiles []string
	for _, item := range tree {
		f, _ := item.(map[string]interface{})
		path, _ := f["path"].(string)
		ftype, _ := f["type"].(string)
		if ftype != "blob" {
			continue
		}
		ext := ""
		if i := strings.LastIndex(path, "."); i != -1 {
			ext = path[i:]
		}
		if ext == ".go" || ext == ".py" || ext == ".ts" || ext == ".js" {
			sourceFiles = append(sourceFiles, path)
		}
		if len(sourceFiles) >= 20 {
			break
		}
	}

	if len(sourceFiles) == 0 {
		return ""
	}

	// ファイル内容を取得 (合計8000文字まで)
	var codeContext strings.Builder
	codeContext.WriteString(fmt.Sprintf("## GitHubリポジトリ: %s/%s\n\n", owner, repo))
	totalLen := 0
	for _, path := range sourceFiles {
		if totalLen >= 8000 {
			break
		}
		fileResp, err := githubRequest("GET",
			fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path),
			headers, nil)
		if err != nil {
			continue
		}
		encoded, _ := fileResp["content"].(string)
		encoded = strings.ReplaceAll(encoded, "\n", "")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		content := string(decoded)
		remaining := 8000 - totalLen
		if len(content) > remaining {
			content = content[:remaining] + "\n...(省略)"
		}
		codeContext.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", path, content))
		totalLen += len(content)
	}

	return codeContext.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ==============================================================================
// Slack Events API エントリーポイント
// ==============================================================================

func handleSlackEvent(w http.ResponseWriter, r *http.Request) {
	// Slackのリトライは無視 (3秒タイムアウトによる重複処理を防ぐ)
	if r.Header.Get("X-Slack-Retry-Num") != "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Slackの署名検証
	if !verifySlackSignature(r.Header, bodyBytes) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Slack URL Verification (初回設定時)
	if payload["type"] == "url_verification" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"challenge": payload["challenge"].(string)})
		return
	}

	event, _ := payload["event"].(map[string]interface{})
	payloadType, _ := payload["type"].(string)
	eventType, _ := event["type"].(string)
	log.Printf("DEBUG: payload_type=%s, event_type=%s", payloadType, eventType)

	if eventType == "app_mention" {
		// 非同期で処理 (Slackは3秒以内のレスポンスが必要)
		go processMention(event)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// ==============================================================================
// Slackメンション処理
// ==============================================================================

func processMention(event map[string]interface{}) {
	channelID, _ := event["channel"].(string)
	threadTS, _ := event["thread_ts"].(string)
	if threadTS == "" {
		threadTS, _ = event["ts"].(string)
	}
	text, _ := event["text"].(string)
	botToken := os.Getenv("SLACK_BOT_TOKEN")

	textLower := strings.ToLower(text)

	switch {
	case strings.Contains(textLower, "fix"):
		messages := getThreadMessages(channelID, threadTS, botToken)
		errorContext := extractErrorContext(messages)
		analysis := analyzeWithClaude(errorContext)

		if getBool(analysis, "should_create_pr") {
			if fix, ok := analysis["fix_suggestion"].(map[string]interface{}); ok && getStr(fix, "description") != "" {
				prURL := createGitHubPR(analysis)
				if prURL != "" {
					postSlackMessage(channelID, threadTS, botToken,
						fmt.Sprintf(":github: *自動修正PRを作成しました*\n%s", prURL))
				}
			}
		}

	case strings.Contains(textLower, "issue"):
		messages := getThreadMessages(channelID, threadTS, botToken)
		errorContext := extractErrorContext(messages)
		analysis := analyzeWithClaude(errorContext)

		issueURL := createGitHubIssue(analysis)
		if issueURL != "" {
			postSlackMessage(channelID, threadTS, botToken,
				fmt.Sprintf(":github: *GitHub Issueを作成しました*\n%s", issueURL))
		} else {
			postSlackMessage(channelID, threadTS, botToken,
				":x: GitHub Issueの作成に失敗しました")
		}

	default:
		postSlackMessage(channelID, threadTS, botToken,
			":wave: こんにちは！使い方:\n• `@relics9-bot fix` - エラーを分析してGitHub PRを作成します\n• `@relics9-bot issue` - エラーをGitHub Issueとして登録します")
	}
}

// ==============================================================================
// Claude APIでエラー分析
// ==============================================================================

func analyzeWithClaude(errorContext string) map[string]interface{} {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))

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

	log.Printf("[Claude IN] Messages.New model=claude-opus-4-6 prompt=%d文字", len(prompt))
	message, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model("claude-opus-4-6")),
		MaxTokens: anthropic.F(int64(2000)),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		}),
	})
	if err != nil {
		log.Printf("[Claude IN] エラー: %v", err)
		return map[string]interface{}{
			"summary":          fmt.Sprintf("Claude API呼び出しエラー: %v", err),
			"should_create_pr": false,
		}
	}

	responseText := message.Content[0].Text
	log.Printf("[Claude OUT] usage=input:%d output:%d tokens response=%s", message.Usage.InputTokens, message.Usage.OutputTokens, truncate(responseText, 200))

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

// resolveRepo はサービス名から実際の GitHub リポジトリ名を解決する。
// 優先順位: 1) REPO_MAP 環境変数 (例: "example-api=example,foo-svc=foo")
//           2) GitHub API でリポジトリ存在確認 + サフィックス除去フォールバック
//           3) GITHUB_REPO 環境変数
func resolveRepo(serviceName string) string {
	owner := os.Getenv("GITHUB_OWNER")
	token := os.Getenv("GITHUB_TOKEN")

	// 1) REPO_MAP から検索
	if repoMap := os.Getenv("REPO_MAP"); repoMap != "" {
		for _, entry := range strings.Split(repoMap, ",") {
			parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == serviceName {
				log.Printf("[resolveRepo] REPO_MAP hit: %s -> %s", serviceName, parts[1])
				return strings.TrimSpace(parts[1])
			}
		}
	}

	// 2) GitHub API でリポジトリを探索
	if owner != "" && token != "" && serviceName != "" {
		headers := githubHeaders(token)
		candidates := []string{serviceName}
		for _, suffix := range []string{"-api", "-service", "-svc", "-app", "-worker"} {
			if strings.HasSuffix(serviceName, suffix) {
				candidates = append(candidates, strings.TrimSuffix(serviceName, suffix))
			}
		}
		for _, candidate := range candidates {
			_, err := githubRequest("GET",
				fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, candidate),
				headers, nil)
			if err == nil {
				log.Printf("[resolveRepo] GitHub API hit: %s -> %s", serviceName, candidate)
				return candidate
			}
		}
	}

	// 3) GITHUB_REPO フォールバック
	fallback := os.Getenv("GITHUB_REPO")
	log.Printf("[resolveRepo] fallback: %s -> %s", serviceName, fallback)
	return fallback
}

// ==============================================================================
// GitHub PR作成
// ==============================================================================

func createGitHubPR(analysis map[string]interface{}) string {
	token := os.Getenv("GITHUB_TOKEN")
	owner := os.Getenv("GITHUB_OWNER")
	repo := resolveRepo(getStr(analysis, "service_name"))
	if repo == "" {
		log.Printf("GitHub PR作成エラー: リポジトリ解決失敗 service_name=%s", getStr(analysis, "service_name"))
		return ""
	}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)

	headers := githubHeaders(token)
	branchName := fmt.Sprintf("fix/auto-%d", time.Now().Unix())

	// デフォルトブランチのSHAを取得
	refResp, err := githubRequest("GET", baseURL+"/git/ref/heads/main", headers, nil)
	if err != nil {
		log.Printf("GitHub ref取得エラー: %v", err)
		return ""
	}
	sha := refResp["object"].(map[string]interface{})["sha"].(string)

	// 新しいブランチを作成
	if _, err := githubRequest("POST", baseURL+"/git/refs", headers, map[string]interface{}{
		"ref": "refs/heads/" + branchName,
		"sha": sha,
	}); err != nil {
		log.Printf("GitHub ブランチ作成エラー: %v", err)
		return ""
	}

	// ファイルが指定されている場合は更新
	if fix, ok := analysis["fix_suggestion"].(map[string]interface{}); ok {
		filePath := getStr(fix, "file_path")
		if filePath != "" && !strings.Contains(filePath, " ") && !strings.Contains(filePath, "(") {
			codeSnippet := getStrOr(fix, "code_snippet", "# Auto-generated fix\n")
			updateData := map[string]interface{}{
				"message": fmt.Sprintf("fix: %s", getStrOr(analysis, "pr_title", "Auto-fix from AI agent")),
				"content": base64.StdEncoding.EncodeToString([]byte(codeSnippet)),
				"branch":  branchName,
			}
			// 既存ファイルのSHAを取得 (更新に必要)
			if existing, err := githubRequest("GET", baseURL+"/contents/"+filePath, headers, nil); err == nil {
				if fileSHA, ok := existing["sha"].(string); ok {
					updateData["sha"] = fileSHA
				}
			}
			if _, err := githubRequest("PUT", baseURL+"/contents/"+filePath, headers, updateData); err != nil {
				log.Printf("GitHub ファイル更新エラー: %v", err)
			}
		}
	}

	// PRを作成
	fix, _ := analysis["fix_suggestion"].(map[string]interface{})
	fixDesc := ""
	if fix != nil {
		fixDesc = getStr(fix, "description")
	}
	prBody := fmt.Sprintf(`## 🤖 AI Agent による自動修正PR

### 根本原因
%s

### 修正内容
%s

### 分析サマリー
%s

---
*このPRはGCPエラーログを検知したAI Agentによって自動作成されました*`,
		getStr(analysis, "root_cause"), fixDesc, getStr(analysis, "summary"))

	prResp, err := githubRequest("POST", baseURL+"/pulls", headers, map[string]interface{}{
		"title": getStrOr(analysis, "pr_title", "fix: Auto-fix from AI agent"),
		"body":  prBody,
		"head":  branchName,
		"base":  "main",
	})
	if err != nil {
		log.Printf("GitHub PR作成エラー: %v", err)
		return ""
	}

	htmlURL, _ := prResp["html_url"].(string)
	return htmlURL
}

// ==============================================================================
// GitHub Issue作成
// ==============================================================================

func createGitHubIssue(analysis map[string]interface{}) string {
	token := os.Getenv("GITHUB_TOKEN")
	owner := os.Getenv("GITHUB_OWNER")
	repo := resolveRepo(getStr(analysis, "service_name"))
	if token == "" || owner == "" || repo == "" {
		log.Printf("GitHub Issue作成エラー: 環境変数未設定 GITHUB_TOKEN=%v GITHUB_OWNER=%v repo=%q service_name=%q",
			token != "", owner != "", repo, getStr(analysis, "service_name"))
		return ""
	}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)

	issueBody := fmt.Sprintf(`## 🤖 AI Agent によるエラー報告

### 根本原因
%s

### 分析サマリー
%s

### 深刻度
%s

---
*このIssueはGCPエラーログを検知したAI Agentによって自動作成されました*`,
		getStr(analysis, "root_cause"),
		getStr(analysis, "summary"),
		getStrOr(analysis, "severity", "unknown"))

	title := getStr(analysis, "pr_title")
	if title == "" {
		title = getStr(analysis, "root_cause")
		if len(title) > 80 {
			title = title[:80]
		}
	}
	if title == "" {
		title = "bug: GCPエラー検知 from AI agent"
	}

	resp, err := githubRequest("POST", baseURL+"/issues", githubHeaders(token), map[string]interface{}{
		"title": title,
		"body":  issueBody,
	})
	if err != nil {
		log.Printf("GitHub Issue作成エラー: %v", err)
		return ""
	}

	htmlURL, _ := resp["html_url"].(string)
	return htmlURL
}

// ==============================================================================
// ユーティリティ関数
// ==============================================================================

func verifySlackSignature(headers http.Header, body []byte) bool {
	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
	if signingSecret == "" {
		log.Println("警告: SLACK_SIGNING_SECRETが未設定です")
		return true
	}

	timestamp := headers.Get("X-Slack-Request-Timestamp")
	signature := headers.Get("X-Slack-Signature")

	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || math.Abs(float64(time.Now().Unix()-ts)) > 300 {
		return false
	}

	sigBase := fmt.Sprintf("v0:%s:%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(sigBase))
	expected := "v0=" + fmt.Sprintf("%x", mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}

func getThreadMessages(channelID, threadTS, botToken string) []map[string]interface{} {
	apiURL := fmt.Sprintf("https://slack.com/api/conversations.replies?channel=%s&ts=%s", channelID, threadTS)
	log.Printf("[Slack IN] conversations.replies channel=%s thread_ts=%s", channelID, threadTS)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Authorization", "Bearer "+botToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Slack IN] エラー: %v", err)
		return nil
	}
	defer resp.Body.Close()

	var data map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&data)

	msgs, _ := data["messages"].([]interface{})
	result := make([]map[string]interface{}, 0, len(msgs))
	for _, m := range msgs {
		if msg, ok := m.(map[string]interface{}); ok {
			result = append(result, msg)
		}
	}
	log.Printf("[Slack IN] conversations.replies -> %d件取得", len(result))
	return result
}

func extractErrorContext(messages []map[string]interface{}) string {
	var texts []string
	for i, msg := range messages {
		if i >= 10 {
			break
		}
		if text, ok := msg["text"].(string); ok && text != "" {
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n\n")
}

func postSlackMessage(channelID, threadTS, botToken, text string) {
	log.Printf("[Slack OUT] chat.postMessage channel=%s thread_ts=%s text=%s", channelID, threadTS, truncate(text, 100))

	data, _ := json.Marshal(map[string]string{
		"channel":   channelID,
		"thread_ts": threadTS,
		"text":      text,
	})

	req, _ := http.NewRequest("POST", "https://slack.com/api/chat.postMessage", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+botToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Slack OUT] エラー: %v", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if ok, _ := result["ok"].(bool); !ok {
		log.Printf("[Slack OUT] エラー: %v", result["error"])
	} else {
		log.Printf("[Slack OUT] 投稿成功")
	}
}

func githubHeaders(token string) map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
		"Content-Type":         "application/json",
	}
}

func githubRequest(method, url string, headers map[string]string, data interface{}) (map[string]interface{}, error) {
	var body io.Reader
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	log.Printf("[GitHub OUT] %s %s", method, url)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[GitHub OUT] エラー: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		limit := 500
		if len(respBody) < limit {
			limit = len(respBody)
		}
		log.Printf("[GitHub OUT] HTTP %d: %s", resp.StatusCode, string(respBody[:limit]))
		return nil, fmt.Errorf("HTTP Error %d: %s", resp.StatusCode, string(respBody[:limit]))
	}

	log.Printf("[GitHub OUT] HTTP %d OK", resp.StatusCode)
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getStrOr(m map[string]interface{}, key, defaultVal string) string {
	if v := getStr(m, key); v != "" {
		return v
	}
	return defaultVal
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func buildLoggingURL(projectID, logName, insertID, timestamp string) string {
	var query string
	if insertID != "" {
		query = fmt.Sprintf(`insertId="%s"`, insertID)
	} else if logName != "" && timestamp != "" {
		query = fmt.Sprintf(`logName="%s" timestamp="%s"`, logName, timestamp)
	} else if logName != "" {
		query = fmt.Sprintf(`logName="%s"`, logName)
	} else {
		query = "severity>=ERROR"
	}
	return fmt.Sprintf(
		"https://console.cloud.google.com/logs/query;query=%s?project=%s",
		url.PathEscape(query),
		projectID,
	)
}
