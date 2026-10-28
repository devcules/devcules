package feedbacklab

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// NotificationEvent represents an alert dispatched across channels
type NotificationEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`      // "milestone_ready", "feedback_submitted", "analysis_complete", "approval_required"
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	ActionURL string    `json:"action_url"`
	Severity  string    `json:"severity"`  // "info", "warning", "critical"
	Timestamp time.Time `json:"timestamp"`
}

// NotificationHub manages in-window broadcast and system notification popping
type NotificationHub struct {
	mu          sync.RWMutex
	subscribers map[chan NotificationEvent]bool
}

func NewNotificationHub() *NotificationHub {
	return &NotificationHub{
		subscribers: make(map[chan NotificationEvent]bool),
	}
}

// Subscribe returns an event channel for SSE streaming to UI windows
func (h *NotificationHub) Subscribe() chan NotificationEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan NotificationEvent, 10)
	h.subscribers[ch] = true
	return ch
}

// Unsubscribe removes a listening client
func (h *NotificationHub) Unsubscribe(ch chan NotificationEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subscribers[ch]; ok {
		delete(h.subscribers, ch)
		close(ch)
	}
}

// PopNotification broadcasts to all browser windows AND triggers system OS alert if on desktop
func (h *NotificationHub) PopNotification(event NotificationEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// 1. Broadcast to open browser windows via SSE
	h.mu.RLock()
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// Non-blocking drop if consumer is slow
		}
	}
	h.mu.RUnlock()

	// 2. Trigger native OS desktop notification pop if running locally
	go popOSNotification(event.Title, event.Message)
}

func popOSNotification(title, message string) {
	switch runtime.GOOS {
	case "darwin":
		// Native macOS Notification Center pop
		script := fmt.Sprintf(`display notification %q with title %q sound name "Ping"`, message, title)
		_ = exec.Command("osascript", "-e", script).Run()
	case "linux":
		// Linux notify-send if installed
		if _, err := exec.LookPath("notify-send"); err == nil {
			_ = exec.Command("notify-send", title, message).Run()
		}
	}
	log.Printf("[NOTIFICATION POP] %s: %s", title, message)
}

// FormatWebPushPayload creates a standard VAPID Web Push payload
func FormatWebPushPayload(event NotificationEvent) ([]byte, error) {
	payload := map[string]any{
		"title": event.Title,
		"body":  event.Message,
		"icon":  "/static/icon.png",
		"badge": "/static/badge.png",
		"data": map[string]string{
			"url":      event.ActionURL,
			"event_id": event.ID,
		},
		"actions": []map[string]string{
			{"action": "open", "title": "Review Now"},
			{"action": "dismiss", "title": "Later"},
		},
	}
	return json.Marshal(payload)
}
