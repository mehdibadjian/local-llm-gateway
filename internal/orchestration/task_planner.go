package orchestration

// ClassifyIntent derives the processing intent from the request.
// Returns (intent, isFallback). isFallback is true only when no explicit
// signal was present and the default was applied.
func ClassifyIntent(req OrchestrationRequest) (Intent, bool) {
	if req.AgentMode {
		return IntentAgentLoop, false
	}
	if req.RAGEnabled && req.Domain != "" {
		return IntentRAGQuery, false
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object" {
		return IntentStructuredOutput, false
	}
	if len(req.Messages) > 0 {
		return IntentSimpleGenerate, false
	}
	// Nothing classifiable — default with fallback flag.
	return IntentSimpleGenerate, true
}
