package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	feedbacklab "github.com/devcules/devcules/lab/feedback"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "7890"
	}

	// 1. Storage setup: check if DATABASE_URL (PostgreSQL) is provided, otherwise use In-Memory
	var store feedbacklab.Store
	pgURL := os.Getenv("DATABASE_URL")
	if pgURL != "" {
		log.Printf("Connecting to PostgreSQL at %s", pgURL)
		s, err := feedbacklab.NewSQLStore("postgres", pgURL)
		if err != nil {
			log.Printf("PostgreSQL connection error (%v), falling back to In-Memory store", err)
			store = feedbacklab.NewMemoryStore()
		} else {
			store = s
		}
	} else {
		log.Println("DATABASE_URL not set; running with instant In-Memory store")
		store = feedbacklab.NewMemoryStore()
	}
	defer store.Close()

	// 2. Seed a sample milestone checkpoint for testing
	ctx := context.Background()
	sampleMilestone := &feedbacklab.MilestoneSnapshot{
		ID:           "ms-checkout-001",
		SessionID:    "sess-live-9281",
		AgentID:      "llama-code-70b",
		Title:        "Milestone 2: Checkout API & Schema Migration",
		Summary:      "Agent implemented checkout order idempotency key and schema migration adding customer_id to orders table.",
		GitCommitSHA: "7a8b9c0d1e",
		FilesChanged: []string{"db/migrations/004_orders_customer.sql", "internal/checkout/handler.go"},
		DiffSnippet: `--- a/db/migrations/004_orders_customer.sql
+++ b/db/migrations/004_orders_customer.sql
@@ -1,3 +1,4 @@
-CREATE TABLE orders (id TEXT PRIMARY KEY, amount INT);
+ALTER TABLE orders ADD COLUMN customer_id TEXT REFERENCES customers(id);
+CREATE INDEX idx_orders_customer ON orders(customer_id);`,
		Status:    "awaiting_feedback",
		CreatedAt: time.Now(),
		Questions: []feedbacklab.Question{
			{
				ID:       "q1_intent",
				Type:     feedbacklab.QuestionTypeLikert,
				Prompt:   "Does this changeset satisfy the accepted requirements?",
				Required: true,
				Weight:   1.0,
			},
			{
				ID:       "q2_risk",
				Type:     feedbacklab.QuestionTypeRisk,
				Prompt:   "Operational Risk Assessment",
				Options:  []string{"Low", "Medium", "High", "Critical"},
				Required: true,
				Weight:   1.5,
			},
			{
				ID:       "q3_edge",
				Type:     feedbacklab.QuestionTypeChoice,
				Prompt:   "Edge Case Completeness",
				Options:  []string{"Yes", "Partially", "No"},
				Required: true,
				Weight:   1.0,
			},
			{
				ID:       "q4_adjust",
				Type:     feedbacklab.QuestionTypeText,
				Prompt:   "Constructive Guidance / Critical Note for Agent",
				Required: false,
				Weight:   1.0,
			},
		},
	}
	if err := store.SaveMilestone(ctx, sampleMilestone); err != nil {
		log.Fatalf("failed to seed milestone: %v", err)
	}

	// 3. Notification Hub & HTTP Server
	hub := feedbacklab.NewNotificationHub()
	srv := feedbacklab.NewServer(store, hub)

	addr := fmt.Sprintf("127.0.0.1:%s", port)
	targetURL := fmt.Sprintf("http://%s", addr)

	log.Println("==================================================================")
	log.Printf("🚀 Devcules Feedback & Sensemaking Lab running on %s", targetURL)
	log.Println("📱 Features enabled: SSE Live Notifications, Question Inquiry,")
	log.Println("   Consensus Indexing, Role Polarity Map, & Context v2 Prompt Delta")
	log.Println("==================================================================")

	// Automatically open the window in the default browser on Mac/Linux
	go func() {
		time.Sleep(400 * time.Millisecond)
		openBrowser(targetURL)
	}()

	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = cmd.Start()
}
