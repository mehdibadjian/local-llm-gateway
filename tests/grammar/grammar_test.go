package grammar_test

import (
	"strings"
	"testing"

	"github.com/caw/wrapper/internal/grammar"
)

func TestLoadGrammar_JSON_NotEmpty(t *testing.T) {
	g, err := grammar.LoadGrammar("json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g == "" {
		t.Fatal("expected non-empty grammar for json")
	}
}

func TestLoadGrammar_Bash_NotEmpty(t *testing.T) {
	g, err := grammar.LoadGrammar("bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if g == "" {
		t.Fatal("expected non-empty grammar for bash")
	}
}

func TestLoadGrammar_Unknown_ReturnsError(t *testing.T) {
	_, err := grammar.LoadGrammar("unknown")
	if err == nil {
		t.Fatal("expected error for unknown grammar")
	}
	if !strings.Contains(err.Error(), "unknown grammar") {
		t.Fatalf("expected error to contain 'unknown grammar', got: %v", err)
	}
}

func TestGrammar_JSON_ContainsRoot(t *testing.T) {
	g, err := grammar.LoadGrammar("json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(g, "root") {
		t.Fatal("expected json grammar to contain 'root'")
	}
}

func TestGrammar_Bash_ContainsRoot(t *testing.T) {
	g, err := grammar.LoadGrammar("bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(g, "root") {
		t.Fatal("expected bash grammar to contain 'root'")
	}
}
