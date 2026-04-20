//go:build benchmark

package benchmark_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caw/wrapper/scripts/benchmark"
)

// TestBenchmarkHarness_DryRun verifies the harness produces a valid result
// without requiring a live Ollama instance (AC-4, AC-5).
func TestBenchmarkHarness_DryRun(t *testing.T) {
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "benchmark_results.json")

	cfg := benchmark.BenchmarkConfig{
		DryRun:     true,
		OutputPath: outPath,
	}
	h := benchmark.NewHarness(cfg)

	result, err := h.RunAndWrite(context.Background())
	if err != nil {
		t.Fatalf("RunAndWrite failed: %v", err)
	}

	if result.MMLU.Total == 0 {
		t.Error("MMLU total should be > 0")
	}
	if result.HumanEval.Total == 0 {
		t.Error("HumanEval total should be > 0")
	}
	if result.RunAt.IsZero() {
		t.Error("RunAt should not be zero")
	}

	// Verify the file was written and is valid JSON.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("report file not found: %v", err)
	}
	var parsed benchmark.BenchmarkResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
}

// TestBenchmarkReport_JSONValid verifies WriteReport / ReadReport round-trip (AC-5).
func TestBenchmarkReport_JSONValid(t *testing.T) {
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "benchmark_results.json")

	want := &benchmark.BenchmarkResult{
		MMLU: benchmark.ScoreResult{
			Score:         80.0,
			Correct:       8,
			Total:         10,
			Baseline:      benchmark.GPT35MMLUBaseline,
			GemmaBaseline: benchmark.GemmaMMLUBaseline,
		},
		HumanEval: benchmark.ScoreResult{
			Score:         60.0,
			Correct:       3,
			Total:         5,
			Baseline:      benchmark.GPT35HumanEvalBaseline,
			GemmaBaseline: benchmark.GemmaHumanEvalBaseline,
		},
		GapClosed: 42.0, // placeholder; overwritten below
		RunAt:     time.Now().UTC(),
	}
	want.GapClosed = benchmark.OverallGapClosed(want)

	if err := benchmark.WriteReport(want, outPath); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	got, err := benchmark.ReadReport(outPath)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}

	if got.MMLU.Correct != want.MMLU.Correct {
		t.Errorf("MMLU.Correct: got %d, want %d", got.MMLU.Correct, want.MMLU.Correct)
	}
	if got.HumanEval.Correct != want.HumanEval.Correct {
		t.Errorf("HumanEval.Correct: got %d, want %d", got.HumanEval.Correct, want.HumanEval.Correct)
	}
	if fmt.Sprintf("%.2f", got.GapClosed) != fmt.Sprintf("%.2f", want.GapClosed) {
		t.Errorf("GapClosed: got %.2f, want %.2f", got.GapClosed, want.GapClosed)
	}
}

// TestGapClosed_Calculation verifies the gap-closed formula (AC-3).
func TestGapClosed_Calculation(t *testing.T) {
	tests := []struct {
		name          string
		caw           float64
		gemmaBaseline float64
		gpt35         float64
		expected      float64
	}{
		{
			name:          "at gemma baseline → 0%",
			caw:           35.0,
			gemmaBaseline: 35.0,
			gpt35:         70.0,
			expected:      0.0,
		},
		{
			name:          "at GPT-3.5 level → 100%",
			caw:           70.0,
			gemmaBaseline: 35.0,
			gpt35:         70.0,
			expected:      100.0,
		},
		{
			name:          "halfway → 50%",
			caw:           52.5,
			gemmaBaseline: 35.0,
			gpt35:         70.0,
			expected:      50.0,
		},
		{
			name:          "zero denominator → 0%",
			caw:           50.0,
			gemmaBaseline: 50.0,
			gpt35:         50.0,
			expected:      0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := benchmark.GapClosedPct(tc.caw, tc.gemmaBaseline, tc.gpt35)
			if fmt.Sprintf("%.4f", got) != fmt.Sprintf("%.4f", tc.expected) {
				t.Errorf("GapClosedPct(%.1f, %.1f, %.1f) = %.4f; want %.4f",
					tc.caw, tc.gemmaBaseline, tc.gpt35, got, tc.expected)
			}
		})
	}
}

// TestMMQU_SampleQuestions verifies MMLU evaluation with a controlled responder (AC-1).
func TestMMQU_SampleQuestions(t *testing.T) {
	// Inject a responder that always returns the correct answer.
	allCorrect := &benchmark.MockResponder{}
	cfg := benchmark.BenchmarkConfig{DryRun: true}
	h := benchmark.NewHarnessWithResponder(cfg, allCorrect)

	result, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.MMLU.Total != len(benchmark.SampleMMLUQuestions) {
		t.Errorf("expected %d MMLU questions, got %d",
			len(benchmark.SampleMMLUQuestions), result.MMLU.Total)
	}
	if result.MMLU.Score < 0 || result.MMLU.Score > 100 {
		t.Errorf("MMLU score out of range: %.2f", result.MMLU.Score)
	}
	// All answers should be correct in dry-run.
	if result.MMLU.Correct != result.MMLU.Total {
		t.Errorf("expected all correct in dry-run, got %d/%d",
			result.MMLU.Correct, result.MMLU.Total)
	}
	if result.MMLU.Score != 100.0 {
		t.Errorf("expected 100%% in dry-run, got %.2f%%", result.MMLU.Score)
	}
}

// TestHumanEval_FunctionDetection verifies ContainsPythonFunctionDef logic (AC-2).
func TestHumanEval_FunctionDetection(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"def sum_list(nums):\n    return sum(nums)\n", true},
		{"  def is_prime(n):\n    pass\n", true},
		{"Here is the function:\ndef foo():\n    pass", true},
		{"This is not a function", false},
		{"deferred := 5", false},
		{"", false},
	}

	for _, tc := range tests {
		got := benchmark.ContainsPythonFunctionDef(tc.input)
		if got != tc.expected {
			t.Errorf("ContainsPythonFunctionDef(%q) = %v; want %v", tc.input, got, tc.expected)
		}
	}
}

// TestHumanEval_MockResponderReturnsValidPython checks that MockResponder returns
// valid Python for HumanEval prompts (AC-2).
func TestHumanEval_MockResponderReturnsValidPython(t *testing.T) {
	r := &benchmark.MockResponder{}
	for _, p := range benchmark.SampleHumanEvalPrompts {
		resp, err := r.GetResponse(context.Background(), p.Prompt)
		if err != nil {
			t.Fatalf("prompt %s: %v", p.ID, err)
		}
		if !benchmark.ContainsPythonFunctionDef(resp) {
			t.Errorf("prompt %s: expected Python def, got: %q", p.ID, resp)
		}
	}
}

// TestBenchmarkConfig_Defaults ensures sensible defaults are applied.
func TestBenchmarkConfig_Defaults(t *testing.T) {
	h := benchmark.NewHarness(benchmark.BenchmarkConfig{DryRun: true})
	if h == nil {
		t.Fatal("NewHarness returned nil")
	}
}

// TestMockResponder_FixedResponse exercises the FixedResponse override.
func TestMockResponder_FixedResponse(t *testing.T) {
	r := &benchmark.MockResponder{FixedResponse: "B"}
	resp, err := r.GetResponse(context.Background(), "any prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "B" {
		t.Errorf("expected 'B', got %q", resp)
	}
}

// TestBenchmarkResult_BaselineConstants verifies embedded baseline constants (AC-3).
func TestBenchmarkResult_BaselineConstants(t *testing.T) {
	if benchmark.GPT35MMLUBaseline != 70.0 {
		t.Errorf("GPT35MMLUBaseline should be 70.0, got %.1f", benchmark.GPT35MMLUBaseline)
	}
	if benchmark.GPT35HumanEvalBaseline != 48.0 {
		t.Errorf("GPT35HumanEvalBaseline should be 48.0, got %.1f", benchmark.GPT35HumanEvalBaseline)
	}
	if benchmark.GemmaMMLUBaseline <= 0 {
		t.Errorf("GemmaMMLUBaseline should be > 0, got %.1f", benchmark.GemmaMMLUBaseline)
	}
	if benchmark.GemmaHumanEvalBaseline <= 0 {
		t.Errorf("GemmaHumanEvalBaseline should be > 0, got %.1f", benchmark.GemmaHumanEvalBaseline)
	}
}

// TestOverallGapClosed_AboveTarget checks the 60% North Star metric path.
func TestOverallGapClosed_AboveTarget(t *testing.T) {
	// Simulate CAW scoring above GPT-3.5 on both benchmarks.
	result := &benchmark.BenchmarkResult{
		MMLU: benchmark.ScoreResult{
			Score:         benchmark.GPT35MMLUBaseline,
			Baseline:      benchmark.GPT35MMLUBaseline,
			GemmaBaseline: benchmark.GemmaMMLUBaseline,
		},
		HumanEval: benchmark.ScoreResult{
			Score:         benchmark.GPT35HumanEvalBaseline,
			Baseline:      benchmark.GPT35HumanEvalBaseline,
			GemmaBaseline: benchmark.GemmaHumanEvalBaseline,
		},
	}
	gap := benchmark.OverallGapClosed(result)
	if gap != 100.0 {
		t.Errorf("expected 100%% gap closed when at GPT-3.5 level, got %.2f", gap)
	}
}

// TestSampleCounts verifies we have the right number of embedded samples.
func TestSampleCounts(t *testing.T) {
	if len(benchmark.SampleMMLUQuestions) != 10 {
		t.Errorf("expected 10 MMLU questions, got %d", len(benchmark.SampleMMLUQuestions))
	}
	if len(benchmark.SampleHumanEvalPrompts) != 5 {
		t.Errorf("expected 5 HumanEval prompts, got %d", len(benchmark.SampleHumanEvalPrompts))
	}
}

// TestDryRun_NoHTTPCalls confirms no real network calls are made in dry-run.
func TestDryRun_NoHTTPCalls(t *testing.T) {
	// This test simply runs with DryRun=true; if any HTTP call were attempted it
	// would fail in CI (no Ollama). A successful result proves no HTTP was used.
	outDir := t.TempDir()
	cfg := benchmark.BenchmarkConfig{
		DryRun:     true,
		Endpoint:   "http://127.0.0.1:19999/v1/chat/completions", // unreachable port
		OutputPath: filepath.Join(outDir, "benchmark_results.json"),
	}
	_, err := benchmark.NewHarness(cfg).RunAndWrite(context.Background())
	if err != nil {
		t.Fatalf("dry-run should not attempt HTTP: %v", err)
	}
}

// TestCustomResponder_PartialScore exercises partial-correct scoring.
func TestCustomResponder_PartialScore(t *testing.T) {
	callCount := 0
	r := &countingResponder{
		fn: func(prompt string) (string, error) {
			callCount++
			// Return correct Python def for HumanEval, wrong answer for MMLU.
			if strings.Contains(prompt, "Python function") {
				return "def foo():\n    pass\n", nil
			}
			return "Z", nil // invalid letter → counted as wrong
		},
	}
	cfg := benchmark.BenchmarkConfig{DryRun: false}
	h := benchmark.NewHarnessWithResponder(cfg, r)

	result, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.MMLU.Correct != 0 {
		t.Errorf("expected 0 MMLU correct, got %d", result.MMLU.Correct)
	}
	if result.HumanEval.Correct != len(benchmark.SampleHumanEvalPrompts) {
		t.Errorf("expected all HumanEval correct, got %d", result.HumanEval.Correct)
	}
	totalCalls := len(benchmark.SampleMMLUQuestions) + len(benchmark.SampleHumanEvalPrompts)
	if callCount != totalCalls {
		t.Errorf("expected %d responder calls, got %d", totalCalls, callCount)
	}
}

// countingResponder wraps a function as a Responder for testing.
type countingResponder struct {
	fn func(prompt string) (string, error)
}

func (c *countingResponder) GetResponse(_ context.Context, prompt string) (string, error) {
	return c.fn(prompt)
}
