package smartguide

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/tingly-dev/tingly-agentscope/pkg/agent"
	"github.com/tingly-dev/tingly-agentscope/pkg/memory"
	"github.com/tingly-dev/tingly-agentscope/pkg/message"
	"github.com/tingly-dev/tingly-agentscope/pkg/model/anthropic"
	"github.com/tingly-dev/tingly-agentscope/pkg/tool"
)

// TinglyBoxAgent is the smart guide agent (@tb)
type TinglyBoxAgent struct {
	*agent.ReActAgent
	config   *SmartGuideConfig
	executor *ToolExecutor
	toolkit  *tool.Toolkit
}

// AgentConfig holds the configuration for creating a TinglyBoxAgent
type AgentConfig struct {
	SmartGuideConfig *SmartGuideConfig
	APIKey           string
	BaseURL          string
	ToolExecutor     *ToolExecutor
	// Callback functions for internal tools
	GetStatusFunc  func(chatID string) (*StatusInfo, error)
	GetProjectFunc func(chatID string) (string, bool, error)
}

// NewTinglyBoxAgent creates a new smart guide agent
func NewTinglyBoxAgent(config *AgentConfig) (*TinglyBoxAgent, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}
	if config.SmartGuideConfig == nil {
		config.SmartGuideConfig = DefaultSmartGuideConfig()
	}

	// Create tool executor if not provided
	executor := config.ToolExecutor
	if executor == nil {
		executor = NewToolExecutor([]string{"cd", "ls", "pwd"})
	}

	// Create toolkit
	toolkit := tool.NewToolkit()

	// Register tools
	if err := RegisterTools(toolkit, executor, config.GetStatusFunc, config.GetProjectFunc); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	// Create model client
	modelConfig := &anthropic.Config{
		ModelName: config.SmartGuideConfig.Model.Model,
		APIKey:    config.APIKey,
	}

	if config.BaseURL != "" {
		modelConfig.BaseURL = config.BaseURL
	}

	modelClient := anthropic.NewClient(modelConfig)

	// Create memory
	mem := memory.NewHistory(100)

	// Create ReActAgent
	reactConfig := &agent.ReActAgentConfig{
		Name:          "tingly-box",
		SystemPrompt:  config.SmartGuideConfig.GetSystemPrompt(),
		Model:         modelClient,
		Toolkit:       toolkit,
		Memory:        mem,
		MaxIterations: config.SmartGuideConfig.MaxIterations,
		Temperature:   &config.SmartGuideConfig.Temperature,
	}

	reactAgent := agent.NewReActAgent(reactConfig)

	return &TinglyBoxAgent{
		ReActAgent: reactAgent,
		config:     config.SmartGuideConfig,
		executor:   executor,
		toolkit:    toolkit,
	}, nil
}

// ReplyWithContext handles a user message with additional context
func (a *TinglyBoxAgent) ReplyWithContext(ctx context.Context, text string, toolCtx *ToolContext) (*message.Msg, error) {
	// Update executor working directory if project path is provided
	if toolCtx != nil && toolCtx.ProjectPath != "" {
		a.executor.SetWorkingDirectory(toolCtx.ProjectPath)
	}

	// Create user message
	userMsg := message.NewMsg(
		"user",
		text,
		"user",
	)

	// Get response
	response, err := a.Reply(ctx, userMsg)
	if err != nil {
		logrus.WithError(err).Error("Failed to get agent response")
		return nil, fmt.Errorf("failed to get response: %w", err)
	}

	return response, nil
}

// GetGreeting returns the default greeting for new users
func (a *TinglyBoxAgent) GetGreeting() string {
	return DefaultGreeting
}

// GetExecutor returns the tool executor
func (a *TinglyBoxAgent) GetExecutor() *ToolExecutor {
	return a.executor
}

// GetToolkit returns the agent's toolkit
func (a *TinglyBoxAgent) GetToolkit() *tool.Toolkit {
	return a.toolkit
}

// IsEnabled returns whether the smart guide is enabled
func (a *TinglyBoxAgent) IsEnabled() bool {
	return a.config != nil && a.config.Enabled
}

// GetConfig returns the agent's configuration
func (a *TinglyBoxAgent) GetConfig() *SmartGuideConfig {
	return a.config
}

// AgentFactory creates TinglyBoxAgent instances
type AgentFactory struct {
	config *SmartGuideConfig
	apiKey string
	baseURL string
}

// NewAgentFactory creates a new agent factory
func NewAgentFactory(config *SmartGuideConfig, apiKey, baseURL string) *AgentFactory {
	return &AgentFactory{
		config:  config,
		apiKey:  apiKey,
		baseURL: baseURL,
	}
}

// CreateAgent creates a new TinglyBoxAgent with the given callbacks
func (f *AgentFactory) CreateAgent(getStatusFunc func(chatID string) (*StatusInfo, error),
	getProjectFunc func(chatID string) (string, bool, error)) (*TinglyBoxAgent, error) {

	return NewTinglyBoxAgent(&AgentConfig{
		SmartGuideConfig: f.config,
		APIKey:           f.apiKey,
		BaseURL:          f.baseURL,
		GetStatusFunc:    getStatusFunc,
		GetProjectFunc:   getProjectFunc,
	})
}
