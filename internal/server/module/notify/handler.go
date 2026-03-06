package notify

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tingly-dev/tingly-box/pkg/notify"
	systemnotify "github.com/tingly-dev/tingly-box/pkg/notify/provider/system"
)

// ClaudeCodeHookInput represents the JSON payload Claude Code sends to hooks via stdin
//
//	{
//	   "session_id": "9db738b8-4ee6-447a-9623-6fbf507e8d90",
//	   "transcript_path": ".claude/projects/-/9db738b8-4ee6-447a-9623-6fbf507e8d90.jsonl",
//	   "cwd": "tingly-box-branch",
//	   "permission_mode": "default",
//	   "hook_event_name": "Stop",
//	   "stop_hook_active": false,
//	   "last_assistant_message": "Hi! I see you're looking at the script files. Need help with something?"
//	}
type ClaudeCodeHookInput struct {
	SessionID            string `json:"session_id"`
	TranscriptPath       string `json:"transcript_path"`
	Cwd                  string `json:"cwd"`
	PermissionMode       string `json:"permission_mode"`
	HookEventName        string `json:"hook_event_name"` // "Stop", "Notification", "PostToolUse", etc.
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"` // the assistant's last message text
	ToolName             string `json:"tool_name"`              // for PostToolUse
	ToolInput            string `json:"tool_input"`             // for PostToolUse
	ToolOutput           string `json:"tool_output"`            // for PostToolUse
	NotificationMessage  string `json:"notification_message"`   // for Notification hook
}

// Handler handles notification HTTP requests from Claude Code hooks
type Handler struct {
	notifier notify.Notifier
}

// NewHandler creates a new notification handler with a system notification provider
func NewHandler() *Handler {
	mux := notify.NewMultiplexer()
	mux.AddProvider(systemnotify.New(systemnotify.Config{AppName: "Tingly Box"}))
	return &Handler{notifier: mux}
}

// Notify receives a Claude Code hook event and sends a desktop notification
// POST /tingly/:scenario/notify
func (h *Handler) Notify(c *gin.Context) {
	var input ClaudeCodeHookInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	title, message := buildMessage(input)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.notifier.Send(ctx, &notify.Notification{
			Title:   title,
			Message: message,
			Level:   notify.LevelInfo,
		})
	}()

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// buildMessage maps Claude Code hook events to notification title/message
func buildMessage(input ClaudeCodeHookInput) (string, string) {
	switch input.HookEventName {
	case "Stop":
		msg := "Task completed"
		if input.LastAssistantMessage != "" {
			// Truncate long messages for notification display
			summary := truncate(input.LastAssistantMessage, 120)
			msg = summary
		}
		return "Claude Code", msg

	case "Notification":
		msg := "Needs attention"
		if input.NotificationMessage != "" {
			msg = input.NotificationMessage
		}
		return "Claude Code", msg

	case "PostToolUse":
		msg := input.ToolName
		if msg == "" {
			msg = "Tool call finished"
		}
		return "Claude Code", msg

	default:
		return "Claude Code", input.HookEventName
	}
}

func truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
