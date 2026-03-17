package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func handleSlackEvent(w http.ResponseWriter, r *http.Request) {
	// Ignore Slack retries to prevent duplicate processing caused by 3-second timeout
	if r.Header.Get("X-Slack-Retry-Num") != "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Verify Slack request signature
	if !verifySlackSignature(r.Header, bodyBytes) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Slack URL verification (initial setup)
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
		// Process asynchronously (Slack requires a response within 3 seconds)
		go processMention(event)
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

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
						fmt.Sprintf(":github: *Auto-fix PR created*\n%s", prURL))
				} else {
					postSlackMessage(channelID, threadTS, botToken,
						":x: Could not auto-create PR: no specific file to fix was identified.\nPlease create a PR manually based on the analysis above.")
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
				fmt.Sprintf(":github: *GitHub Issue created*\n%s", issueURL))
		} else {
			postSlackMessage(channelID, threadTS, botToken,
				":x: Failed to create GitHub Issue")
		}

	default:
		postSlackMessage(channelID, threadTS, botToken,
			fmt.Sprintf(":wave: Hello! Usage:\n• `%s fix` - Analyze error and auto-create a GitHub PR\n• `%s issue` - Register error as a GitHub Issue", os.Getenv("SLACK_BOT_NAME"), os.Getenv("SLACK_BOT_NAME")))
	}
}

func verifySlackSignature(headers http.Header, body []byte) bool {
	signingSecret := os.Getenv("SLACK_SIGNING_SECRET")
	if signingSecret == "" {
		log.Println("Warning: SLACK_SIGNING_SECRET is not set")
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
		log.Printf("[Slack IN] error: %v", err)
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
	log.Printf("[Slack IN] conversations.replies -> %d messages", len(result))
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
		log.Printf("[Slack OUT] error: %v", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	if ok, _ := result["ok"].(bool); !ok {
		log.Printf("[Slack OUT] error: %v", result["error"])
	} else {
		log.Printf("[Slack OUT] message posted successfully")
	}
}
