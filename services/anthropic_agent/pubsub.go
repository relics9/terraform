package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

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

	// 即座に 200 を返して Pub/Sub の再送を防ぐ (Claude/GitHub API は数秒かかるため)
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {

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

	// REPO_MAPに登録されていないサービスはスキップ
	if resolveRepo(serviceName) == "" {
		log.Printf("スキップ: service_name=%q はREPO_MAPに未登録 (severity=%s resource=%s)", serviceName, severity, resourceType)
		return
	}

	// Claudeでエラーを分析
	analysis := analyzeErrorForNotification(severity, resourceType, errorMessage, logEntry)

	// Slackに分析結果を通知
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	owner := os.Getenv("GITHUB_OWNER")

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

	// フィールドを構築
	projectField := fmt.Sprintf("*プロジェクトID:*\n`%s`", projectID)

	githubField := "*GitHub:*\n未特定"
	if owner != "" && serviceName != "" {
		repo := resolveRepo(serviceName)
		if repo != "" {
			githubField = fmt.Sprintf("*GitHub:*\n<https://github.com/%s/%s|%s/%s>", owner, repo, owner, repo)
		}
	}

	serviceField := fmt.Sprintf("*サービス名:*\n`%s`", serviceName)
	if serviceName == "" {
		serviceField = fmt.Sprintf("*サービス名:*\n%s", resourceType)
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
					{"type": "mrkdwn", "text": projectField},
					{"type": "mrkdwn", "text": githubField},
				},
			},
			{
				"type": "section",
				"fields": []map[string]string{
					{"type": "mrkdwn", "text": serviceField},
					{"type": "mrkdwn", "text": "*エラー:*"},
				},
			},
			{
				"type": "actions",
				"elements": []map[string]interface{}{
					{
						"type": "button",
						"text": map[string]string{"type": "plain_text", "text": "Cloud Loggingで確認"},
						"url":   loggingURL,
						"style": "danger",
					},
				},
			},
			{
				"type": "section",
				"text": map[string]string{
					"type": "mrkdwn",
					"text": fmt.Sprintf(":brain: *AI分析:*\n%s", analysis),
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
	}()
}

func analyzeErrorForNotification(severity, resourceType, errorMessage string, logEntry map[string]interface{}) string {
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

	return callClaude(prompt, 1000)
}

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

	// REPO_MAP からリポジトリ名を解決
	repo := resolveRepo(serviceName)
	if repo == "" {
		return ""
	}
	headers := githubHeaders(token)

	// リポジトリのファイルツリーを取得
	treeResp, err := githubRequest("GET",
		fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/HEAD?recursive=1", owner, repo),
		headers, nil)
	if err != nil {
		return ""
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
