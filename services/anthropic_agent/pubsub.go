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

	// Decode Pub/Sub message
	decoded, err := base64.StdEncoding.DecodeString(msg.Message.Data)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Return 200 immediately to prevent Pub/Sub redelivery (Claude/GitHub API calls take several seconds)
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	go func() {

	// Parse log entry
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
			// log/slog uses "msg", other loggers use "message"
			errorMessage = getStr(jp, "msg")
			if errorMessage == "" {
				errorMessage = getStr(jp, "message")
			}
			// Append path/method/query context if available (slog structured fields)
			if method := getStr(jp, "method"); method != "" {
				errorMessage += fmt.Sprintf("\nmethod: %s", method)
			}
			if path := getStr(jp, "path"); path != "" {
				errorMessage += fmt.Sprintf(", path: %s", path)
			}
			if query := getStr(jp, "query"); query != "" {
				errorMessage += fmt.Sprintf(", query: %s", query)
			}
		}
	}
	if errorMessage == "" {
		// Fallback: pass the entire log entry as a JSON string to Claude
		if raw, err := json.Marshal(logEntry); err == nil {
			errorMessage = string(raw)
		} else {
			errorMessage = "unknown error"
		}
	}

	// Identify service name
	serviceName := ""
	for _, key := range []string{"service_name", "function_name", "job_name"} {
		if v, ok := labels[key].(string); ok && v != "" {
			serviceName = v
			break
		}
	}

	// Build a direct link to the log entry in Cloud Logging
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		projectID = "unknown"
	}
	logName := getStr(logEntry, "logName")
	insertID := getStr(logEntry, "insertId")
	timestamp := getStr(logEntry, "timestamp")
	loggingURL := buildLoggingURL(projectID, logName, insertID, timestamp)

	log.Printf("Pub/Sub received: severity=%s, resource=%s, service=%s", severity, resourceType, serviceName)

	// Skip services not registered in REPO_MAP
	if resolveRepo(serviceName) == "" {
		log.Printf("Skipped: service_name=%q is not registered in REPO_MAP (severity=%s resource=%s)", serviceName, severity, resourceType)
		return
	}

	// Send notification to Slack
	webhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	owner := os.Getenv("GITHUB_USER")

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

	// Build fields
	projectField := fmt.Sprintf("*Project ID:*\n`%s`", projectID)

	githubField := "*GitHub:*\nnot identified"
	if owner != "" && serviceName != "" {
		repo := resolveRepo(serviceName)
		if repo != "" {
			githubField = fmt.Sprintf("*GitHub:*\n<https://github.com/%s/%s|%s/%s>", owner, repo, owner, repo)
		}
	}

	serviceField := fmt.Sprintf("*Service:*\n`%s`", serviceName)
	if serviceName == "" {
		serviceField = fmt.Sprintf("*Service:*\n%s", resourceType)
	}

	slackMsg := map[string]interface{}{
	"unfurl_links": false,
	"unfurl_media": false,
	"blocks": []map[string]interface{}{
		{
			"type": "header",
			"text": map[string]string{
				"type": "plain_text",
				"text": fmt.Sprintf("%s GCP Error Detected: %s", emoji, severity),
			},
		},
		{
			"type": "section",
			"fields": []map[string]string{
				{"type": "mrkdwn", "text": projectField},
				{"type": "mrkdwn", "text": githubField},
				{"type": "mrkdwn", "text": serviceField},
			},
		},
		{
			"type": "section",
			"text": map[string]string{
				"type": "mrkdwn",
				"text": fmt.Sprintf("*Error:*\n```%s```", truncate(errorMessage, 300)),
			},
		},
		{
			"type": "actions",
			"elements": []map[string]interface{}{
				{
					"type": "button",
					"text": map[string]string{"type": "plain_text", "text": "View in Cloud Logging"},
					"url":   loggingURL,
					"style": "danger",
				},
			},
		},
		{"type": "divider"},
		{
			"type": "context",
			"elements": []map[string]interface{}{
				{
					"type": "mrkdwn",
					"text": fmt.Sprintf(":robot_face: Mention this bot in the thread:\n• `%s fix` - Analyze & auto-create a GitHub PR _(%s)_\n• `%s issue` - Analyze & create a GitHub Issue _(%s)_", os.Getenv("SLACK_BOT_NAME"), os.Getenv("ANTHROPIC_CLAUDE_MODEL"), os.Getenv("SLACK_BOT_NAME"), os.Getenv("ANTHROPIC_CLAUDE_MODEL")),
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
		log.Printf("Slack notification error: %v", err)
		return
	}

		log.Printf("Slack notification sent: severity=%s, resource=%s", severity, resourceType)
	}()
}


func fetchRepoContext(logEntry map[string]interface{}) string {
	owner := os.Getenv("GITHUB_USER")
	token := os.Getenv("GITHUB_TOKEN")
	if owner == "" || token == "" {
		return ""
	}

	// Identify service name from resource labels
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

	// Resolve repository name from REPO_MAP
	repo := resolveRepo(serviceName)
	if repo == "" {
		return ""
	}
	headers := githubHeaders(token)

	// Fetch repository file tree
	treeResp, err := githubRequest("GET",
		fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/HEAD?recursive=1", owner, repo),
		headers, nil)
	if err != nil {
		return ""
	}

	// Collect source file paths (up to 20)
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

	// Fetch file contents (up to 8000 characters total)
	var codeContext strings.Builder
	codeContext.WriteString(fmt.Sprintf("## GitHub Repository: %s/%s\n\n", owner, repo))
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
			content = content[:remaining] + "\n...(truncated)"
		}
		codeContext.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", path, content))
		totalLen += len(content)
	}

	return codeContext.String()
}
