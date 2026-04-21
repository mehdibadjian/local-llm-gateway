package grammar

import (
	_ "embed"
	"fmt"
)

//go:embed json.gbnf
var jsonGrammar string

//go:embed bash.gbnf
var bashGrammar string

// LoadGrammar returns the GBNF grammar string for the given name ("json" or "bash").
func LoadGrammar(name string) (string, error) {
	switch name {
	case "json":
		return jsonGrammar, nil
	case "bash":
		return bashGrammar, nil
	default:
		return "", fmt.Errorf("unknown grammar: %q", name)
	}
}
