package smart_compact

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/stretchr/testify/assert"
)

func TestClaudeCodeCompactTransformer_ShouldCompactV1_Conditions(t *testing.T) {
	tests := []struct {
		name        string
		messages    []anthropic.MessageParam
		tools       []anthropic.ToolUnionParam
		wantCompact bool
	}{
		{
			name: "compact with tools - should compress",
			messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("hi")),
				anthropic.NewUserMessage(anthropic.NewTextBlock("<command>compact</command>")),
			},
			tools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name: "read_file",
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
						},
					},
				},
			},
			wantCompact: true,
		},
		{
			name: "COMPACT uppercase with tools - should compress (case-insensitive)",
			messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("hi")),
				anthropic.NewUserMessage(anthropic.NewTextBlock("<command>COMPACT</command>")),
			},
			tools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name: "read_file",
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
						},
					},
				},
			},
			wantCompact: true,
		},
		{
			name: "compact without tools - should not compress",
			messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("hi")),
				anthropic.NewUserMessage(anthropic.NewTextBlock("<command>compact</command>")),
			},
			tools:       nil,
			wantCompact: false,
		},
		{
			name: "no compact keyword with tools - should not compress",
			messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("hi")),
				anthropic.NewUserMessage(anthropic.NewTextBlock("regular message")),
			},
			tools: []anthropic.ToolUnionParam{
				{
					OfTool: &anthropic.ToolParam{
						Name: "read_file",
						InputSchema: anthropic.ToolInputSchemaParam{
							Type: "object",
						},
					},
				},
			},
			wantCompact: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transformer := NewClaudeCodeCompactTransformer().(*ClaudeCodeCompactTransformer)
			req := &anthropic.MessageNewParams{}
			req.Messages = tt.messages
			req.Tools = tt.tools
			got := transformer.shouldCompactV1(req)
			assert.Equal(t, tt.wantCompact, got)
		})
	}
}

func TestClaudeCodeCompactStrategy_CompressV1_XMLFormat(t *testing.T) {
	tests := []struct {
		name          string
		input         []anthropic.MessageParam
		expectedCount int
		hasXMLWrapper bool
	}{
		{
			name: "single round - no compression (current round only)",
			input: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("hi")),
			},
			expectedCount: 2,
			hasXMLWrapper: false,
		},
		{
			name: "multiple rounds - compress historical to XML",
			input: []anthropic.MessageParam{
				// Round 1 (historical)
				anthropic.NewUserMessage(anthropic.NewTextBlock("read file")),
				anthropic.NewAssistantMessage(
					anthropic.NewTextBlock("I'll read it"),
					anthropic.NewToolUseBlock("read_file", map[string]any{"path": "file.go"}, "read_file"),
				),
				anthropic.NewUserMessage(anthropic.NewToolResultBlock("read_file", "file content", false)),
				// Round 2 (current)
				anthropic.NewUserMessage(anthropic.NewTextBlock("now do something else")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("OK")),
			},
			expectedCount: 3, // XML summary assistant + current round (2)
			hasXMLWrapper: true,
		},
		{
			name: "multiple historical rounds - all compressed to XML",
			input: []anthropic.MessageParam{
				// Round 1 (historical)
				anthropic.NewUserMessage(anthropic.NewTextBlock("first question")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("first answer")),
				// Round 2 (historical)
				anthropic.NewUserMessage(anthropic.NewTextBlock("second question")),
				anthropic.NewAssistantMessage(
					anthropic.NewTextBlock("second answer"),
					anthropic.NewToolUseBlock("bash", map[string]any{"command": "ls"}, "bash"),
				),
				anthropic.NewUserMessage(anthropic.NewToolResultBlock("bash", "output", false)),
				// Round 3 (current)
				anthropic.NewUserMessage(anthropic.NewTextBlock("current question")),
				anthropic.NewAssistantMessage(anthropic.NewTextBlock("current answer")),
			},
			expectedCount: 3, // XML summary assistant + current round (2)
			hasXMLWrapper: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := NewClaudeCodeCompactStrategy()
			result := strategy.CompressV1(tt.input)
			assert.Equal(t, tt.expectedCount, len(result))

			t.Logf("\n=== Test: %s ===", tt.name)
			t.Logf("Input messages: %d", len(tt.input))
			t.Logf("Output messages: %d", len(result))

			for i, msg := range result {
				t.Logf("  [%d] Role: %s", i, msg.Role)
				text := extractTextFromMessage(msg)
				if len(text) > 0 {
					t.Logf("       Content: %s", truncate(text, 100))
				}
			}

			if tt.hasXMLWrapper {
				// First message should be assistant with XML summary
				assert.Equal(t, "assistant", string(result[0].Role))
				text := extractTextFromMessage(result[0])
				assert.Contains(t, text, "<conversation>")
				assert.Contains(t, text, "</conversation>")

				t.Logf("\n=== XML Output ===\n%s\n=== End XML ===\n", text)
			}
		})
	}
}

func TestClaudeCodeCompactStrategy_XMLStructure(t *testing.T) {
	strategy := NewClaudeCodeCompactStrategy()

	input := []anthropic.MessageParam{
		// Round 1 (historical)
		anthropic.NewUserMessage(anthropic.NewTextBlock("read the files")),
		anthropic.NewAssistantMessage(
			anthropic.NewTextBlock("I'll read them for you"),
			anthropic.NewToolUseBlock("read_file", map[string]any{"path": "src/main.go"}, "1"),
			anthropic.NewToolUseBlock("read_file", map[string]any{"path": "src/utils.go"}, "2"),
		),
		anthropic.NewUserMessage(anthropic.NewToolResultBlock("1", "main content", false)),
		anthropic.NewUserMessage(anthropic.NewToolResultBlock("2", "utils content", false)),
		// Round 2 (current)
		anthropic.NewUserMessage(anthropic.NewTextBlock("compact this")),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock("OK")),
	}

	result := strategy.CompressV1(input)

	// Should have 3 messages: XML summary + current round
	assert.Equal(t, 3, len(result))

	// Check first message is assistant with XML
	assert.Equal(t, "assistant", string(result[0].Role))
	xmlText := extractTextFromMessage(result[0])

	// Print XML output for review
	t.Logf("\n=== XML Output ===\n%s\n=== End XML ===\n", xmlText)

	// Verify XML structure
	assert.Contains(t, xmlText, "Here is the conversation summary:")
	assert.Contains(t, xmlText, "<conversation>")
	assert.Contains(t, xmlText, "</conversation>")

	// Verify user/assistant tags
	assert.Contains(t, xmlText, "<user>")
	assert.Contains(t, xmlText, "</user>")
	assert.Contains(t, xmlText, "<assistant>")
	assert.Contains(t, xmlText, "</assistant>")

	// Verify tool_calls with file paths
	assert.Contains(t, xmlText, "<tool_calls>")
	assert.Contains(t, xmlText, "</tool_calls>")
	assert.Contains(t, xmlText, "<file>src/main.go</file>")
	assert.Contains(t, xmlText, "<file>src/utils.go</file>")

	// Verify original text content is preserved (note: HTML escaping)
	assert.Contains(t, xmlText, "read the files")
	assert.Contains(t, xmlText, "I&#39;ll read them for you")

	// Verify tool_result is NOT included
	assert.NotContains(t, xmlText, "main content")
	assert.NotContains(t, xmlText, "utils content")

	// Check current round is preserved unchanged
	assert.Equal(t, "user", string(result[1].Role))
	assert.Equal(t, "assistant", string(result[2].Role))
}

func TestClaudeCodeCompactStrategy_HTMLEscaping(t *testing.T) {
	strategy := NewClaudeCodeCompactStrategy()

	input := []anthropic.MessageParam{
		// Round 1 (historical) with HTML special characters
		anthropic.NewUserMessage(anthropic.NewTextBlock("Check this <script>")),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock("I see: &lt;content&gt;")),
		// Round 2 (current)
		anthropic.NewUserMessage(anthropic.NewTextBlock("compact")),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock("OK")),
	}

	result := strategy.CompressV1(input)
	xmlText := extractTextFromMessage(result[0])

	// HTML characters should be escaped
	assert.Contains(t, xmlText, "&lt;script&gt;")
	assert.Contains(t, xmlText, "&amp;lt;content&amp;gt;")
}

func TestClaudeCodeCompactStrategy_WithThinkingBlocks(t *testing.T) {
	strategy := NewClaudeCodeCompactStrategy()

	input := []anthropic.MessageParam{
		// Round 1 (historical) with thinking
		anthropic.NewUserMessage(anthropic.NewTextBlock("question")),
		anthropic.NewAssistantMessage(
			anthropic.NewThinkingBlock("sig123", "This is my thinking"),
			anthropic.NewTextBlock("Here is the answer"),
		),
		// Round 2 (current)
		anthropic.NewUserMessage(anthropic.NewTextBlock("compact")),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock("OK")),
	}

	result := strategy.CompressV1(input)
	xmlText := extractTextFromMessage(result[0])

	// Thinking content should NOT be in XML
	assert.NotContains(t, xmlText, "This is my thinking")
	assert.Contains(t, xmlText, "Here is the answer")
}

func TestLastUserMessageContainsCompact(t *testing.T) {
	tests := []struct {
		name     string
		messages []anthropic.MessageParam
		want     bool
	}{
		{
			name: "compact in last message",
			messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
				anthropic.NewUserMessage(anthropic.NewTextBlock("<command>compact</command>")),
			},
			want: true,
		},
		{
			name: "COMPACT uppercase in last message",
			messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
				anthropic.NewUserMessage(anthropic.NewTextBlock("<command>COMPACT</command>")),
			},
			want: true,
		},
		{
			name: "compact not in last message",
			messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("<command>compact</command>")),
				anthropic.NewUserMessage(anthropic.NewTextBlock("hello")),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastUserMessageContainsCompact(tt.messages)
			assert.Equal(t, tt.want, got)
		})
	}
}

// Test for Beta API
func TestClaudeCodeCompactStrategy_CompressBeta_XMLFormat(t *testing.T) {
	strategy := NewClaudeCodeCompactStrategy()

	input := []anthropic.BetaMessageParam{
		// Round 1 (historical)
		{
			Role: anthropic.BetaMessageParamRoleUser,
			Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaTextBlock("read file"),
			},
		},
		{
			Role: anthropic.BetaMessageParamRoleAssistant,
			Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaTextBlock("I'll read it"),
				anthropic.NewBetaToolUseBlock("read_file", map[string]any{"path": "file.go"}, "read_file"),
			},
		},
		{
			Role: anthropic.BetaMessageParamRoleUser,
			Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaToolResultBlock("read_file", "file content", false),
			},
		},
		// Round 2 (current)
		{
			Role: anthropic.BetaMessageParamRoleUser,
			Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaTextBlock("current question"),
			},
		},
		{
			Role: anthropic.BetaMessageParamRoleAssistant,
			Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaTextBlock("current answer"),
			},
		},
	}

	result := strategy.CompressBeta(input)

	// Should have 3 messages: XML summary + current round
	assert.Equal(t, 3, len(result))

	// First message should be assistant with XML
	assert.Equal(t, "assistant", string(result[0].Role))
	xmlText := extractBetaTextFromMessage(result[0])

	assert.Contains(t, xmlText, "<conversation>")
	assert.Contains(t, xmlText, "<user>")
	assert.Contains(t, xmlText, "<assistant>")
	assert.Contains(t, xmlText, "<tool_calls>")
	assert.Contains(t, xmlText, "<file>file.go</file>")

	t.Logf("\n=== Beta XML Output ===\n%s\n=== End Beta XML ===\n", xmlText)
}

// TestClaudeCodeCompactStrategy_RealisticScenario shows a realistic conversation flow
func TestClaudeCodeCompactStrategy_RealisticScenario(t *testing.T) {
	strategy := NewClaudeCodeCompactStrategy()

	input := []anthropic.MessageParam{
		// Round 1: User asks to fix a bug
		anthropic.NewUserMessage(anthropic.NewTextBlock("I have a bug in src/handler.go, can you help?")),
		anthropic.NewAssistantMessage(
			anthropic.NewTextBlock("Let me read the file first."),
			anthropic.NewToolUseBlock("read_file", map[string]any{"path": "src/handler.go"}, "1"),
		),
		anthropic.NewUserMessage(anthropic.NewToolResultBlock("1", "func handleRequest() { ... }", false)),
		anthropic.NewAssistantMessage(
			anthropic.NewTextBlock("I see the issue. You need to add error handling."),
			anthropic.NewToolUseBlock("write_file", map[string]any{"path": "src/handler.go", "content": "fixed code"}, "2"),
		),
		anthropic.NewUserMessage(anthropic.NewToolResultBlock("2", "success", false)),

		// Round 2: User asks about config
		anthropic.NewUserMessage(anthropic.NewTextBlock("Now check the config file")),
		anthropic.NewAssistantMessage(
			anthropic.NewTextBlock("Reading config/config.yaml"),
			anthropic.NewToolUseBlock("read_file", map[string]any{"path": "config/config.yaml"}, "3"),
		),
		anthropic.NewUserMessage(anthropic.NewToolResultBlock("3", "server: localhost:8080", false)),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock("Config looks good!")),

		// Round 3: Current - user asks to compact
		anthropic.NewUserMessage(anthropic.NewTextBlock("Please compact our conversation and continue")),
		anthropic.NewAssistantMessage(anthropic.NewTextBlock("I'll compress and continue...")),
	}

	t.Logf("\n=== Realistic Scenario ===")
	t.Logf("Input messages: %d (3 historical rounds + 1 current round)", len(input))

	result := strategy.CompressV1(input)

	t.Logf("Output messages: %d", len(result))

	// Should have: XML summary + current round (2 messages)
	assert.Equal(t, 3, len(result))

	// First message is the compressed XML summary
	assert.Equal(t, "assistant", string(result[0].Role))
	xmlText := extractTextFromMessage(result[0])

	t.Logf("\n=== Compressed XML Summary ===")
	t.Logf("%s", xmlText)
	t.Logf("=== End Summary ===\n")

	// Verify XML structure contains all three rounds
	assert.Contains(t, xmlText, "<conversation>")
	assert.Contains(t, xmlText, "</conversation>")

	// Verify content from all rounds is present
	assert.Contains(t, xmlText, "bug in src/handler.go")
	assert.Contains(t, xmlText, "add error handling")
	assert.Contains(t, xmlText, "config/config.yaml")

	// Verify file paths are in tool_calls
	assert.Contains(t, xmlText, "<file>src/handler.go</file>")
	assert.Contains(t, xmlText, "<file>config/config.yaml</file>")

	// Verify tool_result content is NOT included
	assert.NotContains(t, xmlText, "func handleRequest()")
	assert.NotContains(t, xmlText, "server: localhost:8080")
	assert.NotContains(t, xmlText, "success")

	// Verify current round is preserved
	assert.Equal(t, "user", string(result[1].Role))
	assert.Equal(t, "assistant", string(result[2].Role))

	t.Logf("=== Current Round Messages ===")
	t.Logf("[1] User: %s", extractTextFromMessage(result[1]))
	t.Logf("[2] Assistant: %s", extractTextFromMessage(result[2]))
}

// Helper functions

func extractTextFromMessage(msg anthropic.MessageParam) string {
	var sb strings.Builder
	for _, block := range msg.Content {
		if block.OfText != nil {
			sb.WriteString(block.OfText.Text)
		}
	}
	return sb.String()
}

func extractBetaTextFromMessage(msg anthropic.BetaMessageParam) string {
	var sb strings.Builder
	for _, block := range msg.Content {
		if block.OfText != nil {
			sb.WriteString(block.OfText.Text)
		}
	}
	return sb.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
