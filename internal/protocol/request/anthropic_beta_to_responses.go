package request

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
	"github.com/sirupsen/logrus"
)

// ConvertAnthropicBetaToResponsesRequest converts Anthropic beta request to OpenAI Responses API format
// The Responses API has a different structure than Chat Completions
func ConvertAnthropicBetaToResponsesRequest(anthropicReq *anthropic.BetaMessageNewParams) responses.ResponseNewParams {
	params := responses.ResponseNewParams{}
	params.Model = shared.ResponsesModel(anthropicReq.Model)

	// Set MaxTokens
	if RequiresMaxCompletionTokens(string(anthropicReq.Model)) {
		params.MaxOutputTokens = openai.Opt(anthropicReq.MaxTokens)
	}

	// Convert system messages to Instructions (system/developer role)
	if len(anthropicReq.System) > 0 {
		params.Instructions = ParamOpt(ConvertBetaTextBlocksToString(anthropicReq.System))
	}

	// Convert messages to Response API Input items
	// Build conversation as a list of input items
	var inputItems []responses.ResponseInputItemUnionParam

	// First pass: collect all tool_use IDs from assistant messages
	// This is needed to validate tool_result blocks in user messages
	toolUseIDs := make(map[string]bool)
	for _, msg := range anthropicReq.Messages {
		if string(msg.Role) == "assistant" {
			for _, block := range msg.Content {
				if block.OfToolUse != nil {
					toolUseIDs[block.OfToolUse.ID] = true
				}
			}
		}
	}

	// Second pass: convert messages to input items
	for _, msg := range anthropicReq.Messages {
		if string(msg.Role) == "user" {
			items := convertBetaUserMessageToResponsesInput(msg, toolUseIDs)
			inputItems = append(inputItems, items...)
		} else if string(msg.Role) == "assistant" {
			items := convertBetaAssistantMessageToResponsesInput(msg)
			inputItems = append(inputItems, items...)
		}
	}

	// Set input - use list format if we have multiple items or complex content
	if len(inputItems) > 0 {
		params.Input = responses.ResponseNewParamsInputUnion{
			OfInputItemList: inputItems,
		}
	} else {
		// Fallback to simple string input
		params.Input = responses.ResponseNewParamsInputUnion{
			OfString: ParamOpt(""),
		}
	}

	// Convert MaxTokens to MaxOutputTokens
	if anthropicReq.MaxTokens > 0 {
		params.MaxOutputTokens = ParamOpt(anthropicReq.MaxTokens)
	}

	// Convert temperature
	if anthropicReq.Temperature.Valid() {
		params.Temperature = ParamOpt(anthropicReq.Temperature.Value)
	}

	// Convert top_p
	if anthropicReq.TopP.Valid() {
		params.TopP = ParamOpt(anthropicReq.TopP.Value)
	}

	// Convert tools from Anthropic format to Responses API format
	if len(anthropicReq.Tools) > 0 {
		params.Tools = ConvertAnthropicBetaToolsToResponses(anthropicReq.Tools)

		// Convert tool choice
		// for some providers (like `vllm`), they require tool choice like `auto` in general usage
		params.ToolChoice = ConvertAnthropicBetaToolChoiceToResponses(&anthropicReq.ToolChoice)
	}

	//// Convert stop sequences
	//if len(anthropicReq.StopSequences) > 0 {
	//	// Responses API uses Stop as a union type
	//	params.Sto = ParamOpt(anthropicReq.StopSequences)
	//}

	return params
}

// convertBetaUserMessageToResponsesInput converts Anthropic beta user message to Responses API input items
// Handles text content and tool_result blocks
// toolUseIDs: map of valid tool_use IDs from previous assistant messages
func convertBetaUserMessageToResponsesInput(msg anthropic.BetaMessageParam, toolUseIDs map[string]bool) []responses.ResponseInputItemUnionParam {
	var items []responses.ResponseInputItemUnionParam

	// Check for tool_result blocks
	var hasToolResult bool
	for _, block := range msg.Content {
		if block.OfToolResult != nil {
			hasToolResult = true
			break
		}
	}

	if hasToolResult {
		// When there are tool_result blocks, we need to create separate items
		for _, block := range msg.Content {
			if block.OfToolResult != nil {
				// Check if this tool_result has a corresponding tool_use in the current request
				toolUseID := block.OfToolResult.ToolUseID
				if !toolUseIDs[toolUseID] {
					// No corresponding tool_use found - convert to text message instead
					logrus.Warnf("[Anthropic→Responses] Skipping tool_result with unmatched tool_use_id=%s (not in current request messages)", toolUseID)

					// Convert tool result to descriptive text
					resultText := convertBetaToolResultContent(block.OfToolResult.Content)
					messageItem := responses.EasyInputMessageParam{
						Type: responses.EasyInputMessageTypeMessage,
						Role: responses.EasyInputMessageRole("user"),
						Content: responses.EasyInputMessageContentUnionParam{
							OfString: ParamOpt(fmt.Sprintf("[Tool Result]: %s", resultText)),
						},
					}
					items = append(items, responses.ResponseInputItemUnionParam{
						OfMessage: &messageItem,
					})
					continue
				}

				// Valid tool_result - convert to function call output
				outputItem := responses.ResponseInputItemFunctionCallOutputParam{
					CallID: toolUseID,
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: ParamOpt(convertBetaToolResultContent(block.OfToolResult.Content)),
					},
					Status: "completed",
				}
				items = append(items, responses.ResponseInputItemUnionParam{
					OfFunctionCallOutput: &outputItem,
				})
			} else if block.OfText != nil {
				// Text content alongside tool results
				messageItem := responses.EasyInputMessageParam{
					Type: responses.EasyInputMessageTypeMessage,
					Role: responses.EasyInputMessageRole("user"),
					Content: responses.EasyInputMessageContentUnionParam{
						OfString: ParamOpt(block.OfText.Text),
					},
				}
				items = append(items, responses.ResponseInputItemUnionParam{
					OfMessage: &messageItem,
				})
			}
		}
	} else {
		// Simple text-only user message
		contentStr := ConvertBetaContentBlocksToString(msg.Content)
		if contentStr != "" {
			messageItem := responses.EasyInputMessageParam{
				Type: responses.EasyInputMessageTypeMessage,
				Role: responses.EasyInputMessageRole("user"),
				Content: responses.EasyInputMessageContentUnionParam{
					OfString: ParamOpt(contentStr),
				},
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &messageItem,
			})
		}
	}

	return items
}

// convertBetaAssistantMessageToResponsesInput converts Anthropic beta assistant message to Responses API input items
// Handles text content, tool_use blocks, and thinking blocks
func convertBetaAssistantMessageToResponsesInput(msg anthropic.BetaMessageParam) []responses.ResponseInputItemUnionParam {
	var items []responses.ResponseInputItemUnionParam
	var textContent string

	// Process content blocks
	for _, block := range msg.Content {
		if block.OfText != nil {
			textContent += block.OfText.Text
		}
	}

	// First, handle tool_use blocks
	for _, block := range msg.Content {
		if block.OfToolUse != nil {
			// Convert tool_use to Responses API function call
			argsJSON, _ := json.Marshal(block.OfToolUse.Input)

			functionCall := responses.ResponseFunctionToolCallParam{
				CallID:    block.OfToolUse.ID,
				Name:      block.OfToolUse.Name,
				Arguments: string(argsJSON),
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfFunctionCall: &functionCall,
			})
		}
	}

	// Add text content as a separate message if present
	// Use ResponseOutputMessage with output_text type for assistant messages
	// to ensure compatibility with strict API providers
	if textContent != "" {
		outputMessage := responses.ResponseOutputMessageParam{
			ID:     "",
			Status: "completed",
			Content: []responses.ResponseOutputMessageContentUnionParam{
				{
					OfOutputText: &responses.ResponseOutputTextParam{
						Text: textContent,
					},
				},
			},
		}
		items = append(items, responses.ResponseInputItemUnionParam{
			OfOutputMessage: &outputMessage,
		})
	}

	// If no items were created, create an empty assistant message
	if len(items) == 0 {
		outputMessage := responses.ResponseOutputMessageParam{
			ID:     "",
			Status: "completed",
			Content: []responses.ResponseOutputMessageContentUnionParam{
				{
					OfOutputText: &responses.ResponseOutputTextParam{
						Text: "",
					},
				},
			},
		}
		items = append(items, responses.ResponseInputItemUnionParam{
			OfOutputMessage: &outputMessage,
		})
	}

	return items
}

// ConvertAnthropicBetaToolsToResponses converts Anthropic beta tools to Responses API format
func ConvertAnthropicBetaToolsToResponses(tools []anthropic.BetaToolUnionParam) []responses.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}

	out := make([]responses.ToolUnionParam, 0, len(tools))

	for _, t := range tools {
		tool := t.OfTool
		if tool == nil {
			continue
		}

		// Convert Anthropic input schema to Responses API function parameters
		// Always initialize parameters to avoid omitting the field (omitzero tag)
		parameters := make(map[string]interface{})
		parameters["type"] = "object"

		if tool.InputSchema.Properties != nil {
			parameters["properties"] = tool.InputSchema.Properties
		} else {
			// Initialize empty properties if none provided
			parameters["properties"] = make(map[string]interface{})
		}

		if len(tool.InputSchema.Required) > 0 {
			parameters["required"] = tool.InputSchema.Required
		}

		// Create function tool
		fn := &responses.FunctionToolParam{
			Name:        tool.Name,
			Description: ParamOpt(tool.Description.Value),
			Parameters:  parameters,
			Type:        "function",
		}

		out = append(out, responses.ToolUnionParam{
			OfFunction: fn,
		})
	}

	return out
}

// ConvertAnthropicBetaToolChoiceToResponses converts Anthropic beta tool_choice to Responses API format
func ConvertAnthropicBetaToolChoiceToResponses(tc *anthropic.BetaToolChoiceUnionParam) responses.ResponseNewParamsToolChoiceUnion {
	// Handle "auto" mode (model decides whether to call tools)
	if tc.OfAuto != nil {
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: ParamOpt(responses.ToolChoiceOptions("auto")),
		}
	}

	// Handle "any" mode (required - force model to call at least one tool)
	if tc.OfAny != nil {
		return responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: ParamOpt(responses.ToolChoiceOptions("required")),
		}
	}

	// Handle specific tool choice
	if tc.OfTool != nil {
		toolParam := responses.ToolChoiceFunctionParam{
			Name: tc.OfTool.Name,
		}
		return responses.ResponseNewParamsToolChoiceUnion{
			OfFunctionTool: &toolParam,
		}
	}

	// Default to auto
	return responses.ResponseNewParamsToolChoiceUnion{
		OfToolChoiceMode: ParamOpt(responses.ToolChoiceOptions("auto")),
	}
}
