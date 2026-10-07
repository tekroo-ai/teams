package agentruntime

// ReasoningConfig belongs to the admitted model profile, not a role name or
// response schema. BudgetTokens is a provider extension; -1 requests no separate
// reasoning cap while MaxTokens still bounds the whole response.
type ReasoningConfig struct {
	EnableThinking bool   `json:"enable_thinking"`
	Effort         string `json:"reasoning_effort,omitempty"`
	BudgetTokens   *int   `json:"reasoning_budget_tokens,omitempty"`
}

func (config ReasoningConfig) Valid() bool {
	if !config.EnableThinking {
		return config.Effort == "" && config.BudgetTokens == nil
	}
	switch config.Effort {
	case "low", "medium", "high", "xhigh":
	default:
		return false
	}
	return config.BudgetTokens == nil || *config.BudgetTokens == -1 || *config.BudgetTokens > 0
}

func (config ReasoningConfig) apply(request map[string]any) {
	request["enable_thinking"] = config.EnableThinking
	template := map[string]any{"enable_thinking": config.EnableThinking}
	if config.EnableThinking {
		request["reasoning_effort"] = config.Effort
		template["reasoning_effort"] = config.Effort
		if config.BudgetTokens != nil {
			request["reasoning_budget_tokens"] = *config.BudgetTokens
		}
	}
	request["chat_template_kwargs"] = template
}
