// Cloud Run Service: Anthropic Agent
// 1. Pub/Sub Push: GCPエラーログをClaudeで分析してSlackに通知 (/notify)
// 2. Slack Events API: @メンションを受けてGitHub PR/Issueを作成 (/)
package main

import (
	"log"
	"net/http"
	"os"
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
