package smart_compact

import (
	"fmt"
	"html"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/sirupsen/logrus"

	"github.com/tingly-dev/tingly-box/internal/protocol"
)

// ClaudeCodeCompactStrategy provides conditional compression for Claude Code compact command.
//
// This strategy:
// - Compresses historical rounds into a single XML-formatted assistant message
// - XML format: <conversation><user>...</user><assistant>...</assistant><tool_calls><file>...</file></tool_calls>...</conversation>
// - Removes all tool_use and tool_result blocks
// - Extracts file paths from tool inputs and includes them in <tool_calls><file>...</file></tool_calls>
// - Keeps only text content from user and assistant messages (removes thinking)
//
// Only activates when:
// 1. Last user message contains "compact" (case-insensitive)
// 2. Request has tool definitions
type ClaudeCodeCompactStrategy struct {
	rounder  *protocol.Grouper
	pathUtil *PathUtil
}

// NewClaudeCodeCompactStrategy creates a new ClaudeCode compact strategy.
func NewClaudeCodeCompactStrategy() *ClaudeCodeCompactStrategy {
	return &ClaudeCodeCompactStrategy{
		rounder:  protocol.NewGrouper(),
		pathUtil: NewPathUtil(),
	}
}

// Name returns the strategy identifier.
func (s *ClaudeCodeCompactStrategy) Name() string {
	return "claude-code-compact"
}

// CompressV1 compresses v1 messages into XML format.
func (s *ClaudeCodeCompactStrategy) CompressV1(messages []anthropic.MessageParam) []anthropic.MessageParam {
	rounds := s.rounder.GroupV1(messages)
	if len(rounds) == 0 {
		return messages
	}

	var result []anthropic.MessageParam
	var xmlBuilder strings.Builder
	xmlBuilder.WriteString("<conversation>")

	for roundIdx, round := range rounds {
		// Current round is the last one - preserve unchanged
		if roundIdx == len(rounds)-1 {
			result = append(result, round.Messages...)
			continue
		}

		// Historical rounds: convert to XML
		s.roundToXML(round, &xmlBuilder)
	}

	xmlBuilder.WriteString("</conversation>")

	// Add XML summary as a single assistant message before current round
	xmlSummary := xmlBuilder.String()
	if len(xmlSummary) > len("<conversation></conversation>") {
		result = append([]anthropic.MessageParam{
			{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("Here is the conversation summary:\n\n" + xmlSummary)},
			},
		}, result...)
	}

	return result
}

// CompressBeta compresses beta messages into XML format.
func (s *ClaudeCodeCompactStrategy) CompressBeta(messages []anthropic.BetaMessageParam) []anthropic.BetaMessageParam {
	rounds := s.rounder.GroupBeta(messages)
	if len(rounds) == 0 {
		return messages
	}

	var result []anthropic.BetaMessageParam
	var xmlBuilder strings.Builder
	xmlBuilder.WriteString("<conversation>")

	for roundIdx, round := range rounds {
		// Current round is the last one - preserve unchanged
		if roundIdx == len(rounds)-1 {
			result = append(result, round.Messages...)
			continue
		}

		// Historical rounds: convert to XML
		s.betaRoundToXML(round, &xmlBuilder)
	}

	xmlBuilder.WriteString("</conversation>")

	// Add XML summary as a single assistant message before current round
	xmlSummary := xmlBuilder.String()
	if len(xmlSummary) > len("<conversation></conversation>") {
		summaryMsg := anthropic.BetaMessageParam{
			Role: anthropic.BetaMessageParamRoleAssistant,
			Content: []anthropic.BetaContentBlockParamUnion{
				{
					OfText: &anthropic.BetaTextBlockParam{
						Type: "text",
						Text: "Here is the conversation summary:\n\n" + xmlSummary,
					},
				},
			},
		}
		result = append([]anthropic.BetaMessageParam{summaryMsg}, result...)
	}

	return result
}

// roundToXML converts a v1 round to XML format.
func (s *ClaudeCodeCompactStrategy) roundToXML(round protocol.V1Round, xmlBuilder *strings.Builder) {
	var collectedFiles []string

	// First pass: collect files from tool_use blocks
	for _, msg := range round.Messages {
		if string(msg.Role) == "assistant" {
			for _, block := range msg.Content {
				if block.OfToolUse != nil {
					if inputMap, ok := block.OfToolUse.Input.(map[string]any); ok {
						files := s.pathUtil.ExtractFromMap(inputMap)
						collectedFiles = append(collectedFiles, files...)
					}
				}
			}
		}
	}

	// Deduplicate files
	collectedFiles = deduplicate(collectedFiles)

	// Build XML
	for _, msg := range round.Messages {
		role := string(msg.Role)

		if role == "user" && s.isPureUserMessage(msg) {
			text := s.extractTextV1(msg)
			if text != "" {
				xmlBuilder.WriteString(fmt.Sprintf("<user>%s</user>\n", html.EscapeString(text)))
			}
		} else if role == "assistant" {
			text := s.extractTextV1(msg)
			xmlBuilder.WriteString(fmt.Sprintf("<assistant>%s</assistant>\n", html.EscapeString(text)))

			// Add tool calls with files
			if len(collectedFiles) > 0 {
				xmlBuilder.WriteString("<tool_calls>")
				for _, file := range collectedFiles {
					xmlBuilder.WriteString(fmt.Sprintf("<file>%s</file>", html.EscapeString(file)))
				}
				xmlBuilder.WriteString("</tool_calls>\n")
			}

			// Clear files after first assistant
			collectedFiles = nil
		}
	}
}

// betaRoundToXML converts a beta round to XML format.
func (s *ClaudeCodeCompactStrategy) betaRoundToXML(round protocol.BetaRound, xmlBuilder *strings.Builder) {
	var collectedFiles []string

	// First pass: collect files from tool_use blocks
	for _, msg := range round.Messages {
		if string(msg.Role) == "assistant" {
			for _, block := range msg.Content {
				if block.OfToolUse != nil {
					if inputMap, ok := block.OfToolUse.Input.(map[string]any); ok {
						files := s.pathUtil.ExtractFromMap(inputMap)
						collectedFiles = append(collectedFiles, files...)
					}
				}
			}
		}
	}

	// Deduplicate files
	collectedFiles = deduplicate(collectedFiles)

	// Build XML
	for _, msg := range round.Messages {
		role := string(msg.Role)

		if role == "user" && s.isPureBetaUserMessage(msg) {
			text := s.extractTextBeta(msg)
			if text != "" {
				xmlBuilder.WriteString(fmt.Sprintf("<user>%s</user>\n", html.EscapeString(text)))
			}
		} else if role == "assistant" {
			text := s.extractTextBeta(msg)
			xmlBuilder.WriteString(fmt.Sprintf("<assistant>%s</assistant>\n", html.EscapeString(text)))

			// Add tool calls with files
			if len(collectedFiles) > 0 {
				xmlBuilder.WriteString("<tool_calls>")
				for _, file := range collectedFiles {
					xmlBuilder.WriteString(fmt.Sprintf("<file>%s</file>", html.EscapeString(file)))
				}
				xmlBuilder.WriteString("</tool_calls>\n")
			}

			// Clear files after first assistant
			collectedFiles = nil
		}
	}
}

// extractTextV1 extracts text content from v1 message.
func (s *ClaudeCodeCompactStrategy) extractTextV1(msg anthropic.MessageParam) string {
	var text strings.Builder
	for _, block := range msg.Content {
		if block.OfText != nil {
			text.WriteString(block.OfText.Text)
		}
	}
	return text.String()
}

// extractTextBeta extracts text content from beta message.
func (s *ClaudeCodeCompactStrategy) extractTextBeta(msg anthropic.BetaMessageParam) string {
	var text strings.Builder
	for _, block := range msg.Content {
		if block.OfText != nil {
			text.WriteString(block.OfText.Text)
		}
	}
	return text.String()
}

// isPureUserMessage checks if message is a pure user message (not tool_result).
func (s *ClaudeCodeCompactStrategy) isPureUserMessage(msg anthropic.MessageParam) bool {
	if string(msg.Role) != "user" {
		return false
	}
	for _, block := range msg.Content {
		if block.OfToolResult != nil {
			return false
		}
	}
	return true
}

// isPureBetaUserMessage checks if beta message is a pure user message.
func (s *ClaudeCodeCompactStrategy) isPureBetaUserMessage(msg anthropic.BetaMessageParam) bool {
	if string(msg.Role) != "user" {
		return false
	}
	for _, block := range msg.Content {
		if block.OfToolResult != nil {
			return false
		}
	}
	return true
}

// ClaudeCodeCompactTransformer provides conditional compression for Claude Code.
//
// This transformer only applies compression when:
// 1. The last user message contains "compact" (case-insensitive)
// 2. The request has tool definitions
//
// Otherwise, it passes the request through unchanged.
type ClaudeCodeCompactTransformer struct {
	strategy *ClaudeCodeCompactStrategy
}

// NewClaudeCodeCompactTransformer creates a new ClaudeCode compact transformer.
func NewClaudeCodeCompactTransformer() protocol.Transformer {
	return &ClaudeCodeCompactTransformer{
		strategy: NewClaudeCodeCompactStrategy(),
	}
}

// HandleV1 handles compacting for Anthropic v1 requests.
func (t *ClaudeCodeCompactTransformer) HandleV1(req *anthropic.MessageNewParams) error {
	if req.Messages == nil || len(req.Messages) == 0 {
		return nil
	}

	// Check if we should apply compression
	if !t.shouldCompactV1(req) {
		logrus.Debugf("[claude-code-compact] v1: conditions not met, passing through")
		return nil
	}

	logrus.Infof("[claude-code-compact] v1: applying compression")
	req.Messages = t.strategy.CompressV1(req.Messages)
	return nil
}

// HandleV1Beta handles compacting for Anthropic v1beta requests.
func (t *ClaudeCodeCompactTransformer) HandleV1Beta(req *anthropic.BetaMessageNewParams) error {
	if req.Messages == nil || len(req.Messages) == 0 {
		return nil
	}

	// Check if we should apply compression
	if !t.shouldCompactV1Beta(req) {
		logrus.Debugf("[claude-code-compact] v1beta: conditions not met, passing through")
		return nil
	}

	logrus.Infof("[claude-code-compact] v1beta: applying compression")
	req.Messages = t.strategy.CompressBeta(req.Messages)
	return nil
}

// shouldCompactV1 checks v1 conditions for compression.
func (t *ClaudeCodeCompactTransformer) shouldCompactV1(req *anthropic.MessageNewParams) bool {
	// Condition 1: Must have tools
	if req.Tools == nil || len(req.Tools) == 0 {
		return false
	}

	// Condition 2: Last user message must contain "compact" (case-insensitive)
	return lastUserMessageContainsCompact(req.Messages)
}

// shouldCompactV1Beta checks v1beta conditions for compression.
func (t *ClaudeCodeCompactTransformer) shouldCompactV1Beta(req *anthropic.BetaMessageNewParams) bool {
	// Condition 1: Must have tools
	if req.Tools == nil || len(req.Tools) == 0 {
		return false
	}

	// Condition 2: Last user message must contain "compact" (case-insensitive)
	return lastUserMessageContainsCompactBeta(req.Messages)
}

// lastUserMessageContainsCompact checks if the last user message contains "compact" (case-insensitive).
func lastUserMessageContainsCompact(messages []anthropic.MessageParam) bool {
	// Find the last user message
	var lastUserMsg anthropic.MessageParam
	for i := len(messages) - 1; i >= 0; i-- {
		if string(messages[i].Role) == "user" {
			lastUserMsg = messages[i]
			break
		}
	}

	// Extract text content and check for "compact" (case-insensitive)
	var textContent strings.Builder
	for _, block := range lastUserMsg.Content {
		if block.OfText != nil {
			textContent.WriteString(block.OfText.Text)
		}
	}

	return strings.Contains(strings.ToLower(textContent.String()), "compact")
}

// lastUserMessageContainsCompactBeta checks if the last user message contains "compact" (case-insensitive) for beta API.
func lastUserMessageContainsCompactBeta(messages []anthropic.BetaMessageParam) bool {
	// Find the last user message
	var lastUserMsg anthropic.BetaMessageParam
	for i := len(messages) - 1; i >= 0; i-- {
		if string(messages[i].Role) == "user" {
			lastUserMsg = messages[i]
			break
		}
	}

	// Extract text content and check for "compact" (case-insensitive)
	var textContent strings.Builder
	for _, block := range lastUserMsg.Content {
		if block.OfText != nil {
			textContent.WriteString(block.OfText.Text)
		}
	}

	return strings.Contains(strings.ToLower(textContent.String()), "compact")
}
