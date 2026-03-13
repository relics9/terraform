// Cloud Run Service: Anthropic Agent
// 1. Pub/Sub Push: Analyzes GCP error logs with Claude and sends Slack notifications (/notify)
// 2. Slack Events API: Responds to @mentions to create GitHub PRs/Issues (/)
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

	http.HandleFunc("/notify", handlePubSubNotify) // Pub/Sub Push subscription
	http.HandleFunc("/", handleSlackEvent)          // Slack Events API
	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
