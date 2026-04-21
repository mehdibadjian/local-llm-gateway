package benchmark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// BenchmarkConfig controls how the harness is run.
type BenchmarkConfig struct {
	// Endpoint is the CAW chat completions URL.
	Endpoint string
	// APIKey is sent as a Bearer token; read from CAW_API_KEY when empty.
	APIKey string
	// DryRun uses mock responses so no live Ollama instance is required.
	DryRun bool
	// OutputPath is where benchmark_results.json is written.
	OutputPath string
	// Model is the model name forwarded to the endpoint (default: gemma:2b).
	Model string
}

// Responder abstracts the mechanism used to get a completion for a prompt.
type Responder interface {
	GetResponse(ctx context.Context, prompt string) (string, error)
}

// MockResponder returns canned correct answers for all benchmark questions.
// It is used in dry-run mode and in tests.
type MockResponder struct {
	// FixedResponse overrides the default answer when non-empty.
	FixedResponse string
}

// GetResponse returns the FixedResponse when set; otherwise echoes the correct
// answer letter for MMLU questions and a valid Python def for HumanEval prompts.
func (m *MockResponder) GetResponse(_ context.Context, prompt string) (string, error) {
	if m.FixedResponse != "" {
		return m.FixedResponse, nil
	}
	// HumanEval prompts ask to "Write a Python function"
	if strings.Contains(prompt, "Python function") {
		return "def solution():\n    pass\n", nil
	}
	// Look up the correct answer by matching the question text in the prompt.
	for _, q := range SampleMMLUQuestions {
		if strings.Contains(prompt, q.Question) {
			return q.Answer, nil
		}
	}
	return "A", nil
}

// openAIRequest is the minimal JSON body sent to the CAW endpoint.
type openAIRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// HTTPResponder calls the live CAW /v1/chat/completions endpoint.
type HTTPResponder struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

// GetResponse calls the CAW HTTP endpoint.
func (h *HTTPResponder) GetResponse(ctx context.Context, prompt string) (string, error) {
	body := openAIRequest{
		Model:    h.model,
		Messages: []message{{Role: "user", Content: prompt}},
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.endpoint, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if h.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+h.apiKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http call: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("endpoint returned %d: %s", resp.StatusCode, raw)
	}
	var out openAIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty choices in response")
	}
	return out.Choices[0].Message.Content, nil
}

// Harness orchestrates the full benchmark run.
type Harness struct {
	config    BenchmarkConfig
	responder Responder
}

// NewHarness creates a Harness, selecting the appropriate Responder.
func NewHarness(config BenchmarkConfig) *Harness {
	if config.Endpoint == "" {
		config.Endpoint = "http://localhost:8080/v1/chat/completions"
	}
	if config.Model == "" {
		config.Model = "gemma:2b"
	}
	if config.APIKey == "" {
		config.APIKey = os.Getenv("CAW_API_KEY")
	}

	var r Responder
	if config.DryRun {
		r = &MockResponder{}
	} else {
		r = &HTTPResponder{
			endpoint: config.Endpoint,
			apiKey:   config.APIKey,
			model:    config.Model,
			client:   &http.Client{Timeout: 30 * time.Second},
		}
	}
	return &Harness{config: config, responder: r}
}

// NewHarnessWithResponder creates a Harness with a caller-supplied Responder.
// This is the primary injection point for unit tests.
func NewHarnessWithResponder(config BenchmarkConfig, r Responder) *Harness {
	if config.Endpoint == "" {
		config.Endpoint = "http://localhost:8080/v1/chat/completions"
	}
	if config.Model == "" {
		config.Model = "gemma:2b"
	}
	return &Harness{config: config, responder: r}
}

// Run executes the full MMLU + HumanEval benchmark and returns a BenchmarkResult.
func (h *Harness) Run(ctx context.Context) (*BenchmarkResult, error) {
	mmlu, err := h.runMMQU(ctx)
	if err != nil {
		return nil, fmt.Errorf("mmlu run: %w", err)
	}
	he, err := h.runHumanEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("humaneval run: %w", err)
	}

	result := &BenchmarkResult{
		MMLU:      mmlu,
		HumanEval: he,
		RunAt:     time.Now().UTC(),
	}
	result.GapClosed = OverallGapClosed(result)
	return result, nil
}

// RunAndWrite runs the benchmark and writes the JSON report to OutputPath.
func (h *Harness) RunAndWrite(ctx context.Context) (*BenchmarkResult, error) {
	result, err := h.Run(ctx)
	if err != nil {
		return nil, err
	}
	outputPath := h.config.OutputPath
	if outputPath == "" {
		outputPath = "scripts/benchmark/benchmark_results.json"
	}
	if err := WriteReport(result, outputPath); err != nil {
		return nil, err
	}
	return result, nil
}

// runMMQU iterates over SampleMMLUQuestions and tallies correct answers.
func (h *Harness) runMMQU(ctx context.Context) (ScoreResult, error) {
	correct := 0
	total := len(SampleMMLUQuestions)
	for _, q := range SampleMMLUQuestions {
		prompt := buildMMLUPrompt(q)
		response, err := h.responder.GetResponse(ctx, prompt)
		if err != nil {
			return ScoreResult{}, fmt.Errorf("question %q: %w", q.Question, err)
		}
		if extractAnswerLetter(response) == q.Answer {
			correct++
		}
	}
	score := float64(correct) / float64(total) * 100
	return ScoreResult{
		Score:         score,
		Correct:       correct,
		Total:         total,
		Baseline:      GPT35MMLUBaseline,
		GemmaBaseline: GemmaMMLUBaseline,
	}, nil
}

// runHumanEval iterates over SampleHumanEvalPrompts and checks for valid Python defs.
func (h *Harness) runHumanEval(ctx context.Context) (ScoreResult, error) {
	correct := 0
	total := len(SampleHumanEvalPrompts)
	for _, p := range SampleHumanEvalPrompts {
		response, err := h.responder.GetResponse(ctx, p.Prompt)
		if err != nil {
			return ScoreResult{}, fmt.Errorf("prompt %s: %w", p.ID, err)
		}
		if ContainsPythonFunctionDef(response) {
			correct++
		}
	}
	score := float64(correct) / float64(total) * 100
	return ScoreResult{
		Score:         score,
		Correct:       correct,
		Total:         total,
		Baseline:      GPT35HumanEvalBaseline,
		GemmaBaseline: GemmaHumanEvalBaseline,
	}, nil
}

// buildMMLUPrompt constructs a prompt for a MMLU question.
func buildMMLUPrompt(q MMLUQuestion) string {
	var sb strings.Builder
	sb.WriteString("Answer the following multiple-choice question with only the letter of the correct answer (A, B, C, or D).\n\n")
	sb.WriteString("Question: ")
	sb.WriteString(q.Question)
	sb.WriteString("\n")
	for _, k := range []string{"A", "B", "C", "D"} {
		if v, ok := q.Choices[k]; ok {
			fmt.Fprintf(&sb, "%s) %s\n", k, v)
		}
	}
	sb.WriteString("\nAnswer:")
	return sb.String()
}

// extractAnswerLetter pulls the first A/B/C/D occurrence from an LLM response.
func extractAnswerLetter(response string) string {
	response = strings.TrimSpace(response)
	re := regexp.MustCompile(`\b([A-D])\b`)
	if m := re.FindStringSubmatch(response); len(m) == 2 {
		return m[1]
	}
	// Last-resort: first character if it's a letter.
	if len(response) > 0 {
		ch := strings.ToUpper(string(response[0]))
		if ch >= "A" && ch <= "D" {
			return ch
		}
	}
	return ""
}

// ContainsPythonFunctionDef reports whether s contains a Python function definition.
// Matches both line-initial "def foo():" and inline "Answer: def foo():" patterns.
func ContainsPythonFunctionDef(s string) bool {
	return regexp.MustCompile(`(?m)(^\s*def\s+\w+\s*\(|[^a-z]def\s+\w+\s*\()`).MatchString(s)
}
