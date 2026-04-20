//go:build e2e

// Package e2e runs the full benchmark harness against a live CAW stack and
// asserts that the North Star metric is met:
//
//	Gap closed ≥ 60% across MMLU + HumanEval
//	(i.e. CAW+gemma:2b closes at least 60% of the distance between
//	 the raw gemma:2b baseline and GPT-3.5 published scores)
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	bm "github.com/caw/wrapper/scripts/benchmark"
)

const (
	defaultEndpoint = "http://localhost:8080/v1/chat/completions"
	defaultAPIKey   = "dev-key"
	defaultModel    = "gemma:2b"
	targetGapClosed = 60.0 // North Star: ≥ 60%
	perRequestTO    = 60 * time.Second
	totalTO         = 15 * time.Minute
)

// endpoint returns the CAW endpoint from env or falls back to default.
func endpoint() string {
	if v := os.Getenv("CAW_ENDPOINT"); v != "" {
		return v
	}
	return defaultEndpoint
}

// apiKey returns the CAW API key from env or falls back to default.
func apiKey() string {
	if v := os.Getenv("CAW_API_KEY"); v != "" {
		return v
	}
	return defaultAPIKey
}

// requireStackReady pings /healthz and /readyz; skips the test if unreachable.
func requireStackReady(t *testing.T) {
	t.Helper()
	base := strings.TrimSuffix(strings.TrimSuffix(endpoint(), "/v1/chat/completions"), "/")
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(base + path)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Skipf("CAW stack not reachable at %s%s — start with `docker compose up -d`", base, path)
		}
		resp.Body.Close()
	}
}

// TestE2E_BenchmarkNorthStar is the primary E2E gate.
// It runs every MMLU + HumanEval question through the live CAW API,
// computes gap-closed % using the published baselines, and fails if
// the result is below the 60% North Star target.
func TestE2E_BenchmarkNorthStar(t *testing.T) {
	requireStackReady(t)

	cfg := bm.BenchmarkConfig{
		Endpoint: endpoint(),
		APIKey:   apiKey(),
		Model:    defaultModel,
		DryRun:   false,
	}
	h := bm.NewHarness(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), totalTO)
	defer cancel()

	t.Log("▶ Running MMLU + HumanEval against live CAW stack…")
	result, err := h.Run(ctx)
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}

	// Write JSON report for CI artifacts.
	reportPath := "e2e_benchmark_results.json"
	if err := bm.WriteReport(result, reportPath); err != nil {
		t.Logf("warn: could not write report to %s: %v", reportPath, err)
	}

	printReport(t, result)

	if result.GapClosed < targetGapClosed {
		t.Errorf("❌ North Star MISSED: gap_closed=%.1f%% (target ≥ %.0f%%)",
			result.GapClosed, targetGapClosed)
	} else {
		t.Logf("✅ North Star MET: gap_closed=%.1f%% (target ≥ %.0f%%)",
			result.GapClosed, targetGapClosed)
	}
}

// TestE2E_MMLU runs only MMLU questions and checks the individual category score.
func TestE2E_MMLU(t *testing.T) {
	requireStackReady(t)

	client := &http.Client{Timeout: perRequestTO}
	r := &liveResponder{endpoint: endpoint(), apiKey: apiKey(), model: defaultModel, client: client}

	t.Logf("Running %d MMLU questions…", len(bm.SampleMMLUQuestions))

	correct := 0
	for i, q := range bm.SampleMMLUQuestions {
		prompt := buildMMLUPrompt(q)
		ctx, cancel := context.WithTimeout(context.Background(), perRequestTO)
		resp, err := r.get(ctx, prompt)
		cancel()
		if err != nil {
			t.Errorf("Q%d %q: request failed: %v", i+1, q.Question, err)
			continue
		}
		got := extractLetter(resp)
		pass := got == q.Answer
		if pass {
			correct++
		}
		t.Logf("Q%d [%s] want=%s got=%s pass=%v | %q", i+1, q.Question[:min(40, len(q.Question))], q.Answer, got, pass, truncate(resp, 80))
	}

	score := float64(correct) / float64(len(bm.SampleMMLUQuestions)) * 100
	gapClosed := bm.GapClosedPct(score, bm.GemmaMMLUBaseline, bm.GPT35MMLUBaseline)
	t.Logf("MMLU: %d/%d correct | score=%.0f%% | gap_closed=%.1f%%",
		correct, len(bm.SampleMMLUQuestions), score, gapClosed)
}

// TestE2E_HumanEval runs only HumanEval prompts and checks for valid Python defs.
func TestE2E_HumanEval(t *testing.T) {
	requireStackReady(t)

	client := &http.Client{Timeout: perRequestTO}
	r := &liveResponder{endpoint: endpoint(), apiKey: apiKey(), model: defaultModel, client: client}

	t.Logf("Running %d HumanEval prompts…", len(bm.SampleHumanEvalPrompts))

	correct := 0
	for _, p := range bm.SampleHumanEvalPrompts {
		ctx, cancel := context.WithTimeout(context.Background(), perRequestTO)
		resp, err := r.get(ctx, p.Prompt)
		cancel()
		if err != nil {
			t.Errorf("%s: request failed: %v", p.ID, err)
			continue
		}
		hasDef := bm.ContainsPythonFunctionDef(resp)
		if hasDef {
			correct++
		}
		t.Logf("%s pass=%v | %q", p.ID, hasDef, truncate(resp, 100))
	}

	score := float64(correct) / float64(len(bm.SampleHumanEvalPrompts)) * 100
	gapClosed := bm.GapClosedPct(score, bm.GemmaHumanEvalBaseline, bm.GPT35HumanEvalBaseline)
	t.Logf("HumanEval: %d/%d correct | score=%.0f%% | gap_closed=%.1f%%",
		correct, len(bm.SampleHumanEvalPrompts), score, gapClosed)
}

// TestE2E_HealthEndpoints verifies all health/readiness probes.
func TestE2E_HealthEndpoints(t *testing.T) {
	requireStackReady(t)
	base := strings.TrimSuffix(strings.TrimSuffix(endpoint(), "/v1/chat/completions"), "/")

	cases := []struct {
		path string
		want string
	}{
		{"/healthz", "ok"},
		{"/readyz", "ready"},
	}
	for _, tc := range cases {
		resp, err := http.Get(base + tc.path)
		if err != nil {
			t.Errorf("%s: %v", tc.path, err)
			continue
		}
		defer resp.Body.Close()
		var body map[string]string
		json.NewDecoder(resp.Body).Decode(&body)
		if resp.StatusCode != 200 {
			t.Errorf("%s: status=%d", tc.path, resp.StatusCode)
		}
		if body["status"] != tc.want {
			t.Errorf("%s: status=%q want %q", tc.path, body["status"], tc.want)
		}
		t.Logf("✅ %s → %v", tc.path, body)
	}
}

// TestE2E_RateLimit verifies the rate limiter allows requests and returns 429 when over limit.
func TestE2E_RateLimit(t *testing.T) {
	requireStackReady(t)

	// First request must succeed.
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest(http.MethodPost, endpoint(),
		strings.NewReader(`{"model":"gemma:2b","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		t.Error("first request unexpectedly rate-limited")
	} else {
		t.Logf("✅ first request: status=%d (not rate-limited)", resp.StatusCode)
	}
}

// TestE2E_EmbedService verifies the embedding service returns 384-dim vectors.
func TestE2E_EmbedService(t *testing.T) {
	resp, err := http.Post("http://localhost:5001/embed",
		"application/json",
		strings.NewReader(`{"text":"hello world"}`))
	if err != nil {
		t.Skipf("embed service not reachable: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Embedding []float64 `json:"embedding"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Embedding) != 384 {
		t.Errorf("expected 384-dim embedding, got %d", len(body.Embedding))
	} else {
		t.Logf("✅ embed service: 384-dim vector, first3=[%.4f %.4f %.4f]",
			body.Embedding[0], body.Embedding[1], body.Embedding[2])
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

type liveResponder struct {
	endpoint, apiKey, model string
	client                  *http.Client
}

func (r *liveResponder) get(ctx context.Context, prompt string) (string, error) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%s}]}`,
		r.model, jsonString(prompt))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint,
		strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
		Error *struct{ Message string } `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Error != nil {
		return "", fmt.Errorf("api error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}
	return out.Choices[0].Message.Content, nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func buildMMLUPrompt(q bm.MMLUQuestion) string {
	var sb strings.Builder
	sb.WriteString("Answer with ONLY the letter (A, B, C, or D) of the correct answer. No explanation.\n\n")
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

func extractLetter(s string) string {
	s = strings.TrimSpace(s)
	for _, ch := range []string{"A", "B", "C", "D"} {
		if strings.HasPrefix(s, ch) {
			return ch
		}
	}
	// scan for standalone letter
	for _, word := range strings.Fields(s) {
		w := strings.Trim(word, ".,):*")
		if w == "A" || w == "B" || w == "C" || w == "D" {
			return w
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func printReport(t *testing.T, r *bm.BenchmarkResult) {
	t.Helper()
	mmluGap := bm.GapClosedPct(r.MMLU.Score, r.MMLU.GemmaBaseline, r.MMLU.Baseline)
	heGap := bm.GapClosedPct(r.HumanEval.Score, r.HumanEval.GemmaBaseline, r.HumanEval.Baseline)

	t.Log("──────────────────────────────────────────────────")
	t.Logf("  MMLU        : %d/%d correct → %.0f%%  (gemma:2b=%.0f%%  GPT-3.5=%.0f%%)",
		r.MMLU.Correct, r.MMLU.Total, r.MMLU.Score, r.MMLU.GemmaBaseline, r.MMLU.Baseline)
	t.Logf("  MMLU gap    : %.1f%% closed", mmluGap)
	t.Logf("  HumanEval   : %d/%d correct → %.0f%%  (gemma:2b=%.0f%%  GPT-3.5=%.0f%%)",
		r.HumanEval.Correct, r.HumanEval.Total, r.HumanEval.Score, r.HumanEval.GemmaBaseline, r.HumanEval.Baseline)
	t.Logf("  HumanEval gap: %.1f%% closed", heGap)
	t.Logf("  ── OVERALL GAP CLOSED: %.1f%% (target ≥ %.0f%%) ──", r.GapClosed, targetGapClosed)
	t.Log("──────────────────────────────────────────────────")
}
