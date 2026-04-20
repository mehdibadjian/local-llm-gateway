package benchmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// GPT-3.5 published benchmark scores used as the upper target.
	GPT35MMLUBaseline      = 70.0
	GPT35HumanEvalBaseline = 48.0

	// Gemma:2b published benchmark scores used as the lower baseline.
	GemmaMMLUBaseline      = 35.0
	GemmaHumanEvalBaseline = 12.0
)

// ScoreResult holds the outcome for a single benchmark category.
type ScoreResult struct {
	Score         float64 `json:"score"`
	Correct       int     `json:"correct"`
	Total         int     `json:"total"`
	Baseline      float64 `json:"gpt35_baseline"`
	GemmaBaseline float64 `json:"gemma_baseline"`
}

// BenchmarkResult is the top-level report written to disk.
type BenchmarkResult struct {
	MMLU      ScoreResult `json:"mmlu"`
	HumanEval ScoreResult `json:"humaneval"`
	GapClosed float64     `json:"gap_closed_pct"`
	RunAt     time.Time   `json:"run_at"`
}

// GapClosedPct returns the percentage of the capability gap closed for a single category.
// Formula: (caw_score - gemma_baseline) / (gpt35_baseline - gemma_baseline) * 100
// Returns 0 when the denominator is zero.
func GapClosedPct(cawScore, gemmaBaseline, gpt35Baseline float64) float64 {
	denom := gpt35Baseline - gemmaBaseline
	if denom == 0 {
		return 0
	}
	return (cawScore - gemmaBaseline) / denom * 100
}

// OverallGapClosed averages the gap-closed percentages across MMLU and HumanEval.
func OverallGapClosed(r *BenchmarkResult) float64 {
	mmluGap := GapClosedPct(r.MMLU.Score, r.MMLU.GemmaBaseline, r.MMLU.Baseline)
	heGap := GapClosedPct(r.HumanEval.Score, r.HumanEval.GemmaBaseline, r.HumanEval.Baseline)
	return (mmluGap + heGap) / 2
}

// WriteReport serialises result to JSON and writes it to outputPath.
// Parent directories are created as needed.
func WriteReport(result *BenchmarkResult, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// ReadReport deserialises a previously written JSON report.
func ReadReport(path string) (*BenchmarkResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	var r BenchmarkResult
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("unmarshal report: %w", err)
	}
	return &r, nil
}
