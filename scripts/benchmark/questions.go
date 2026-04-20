package benchmark

// MMLUQuestion is a single multiple-choice question with a correct answer letter.
type MMLUQuestion struct {
	Question string
	Choices  map[string]string
	Answer   string // "A", "B", "C", or "D"
}

// HumanEvalPrompt is a natural-language prompt asking for a Python function.
type HumanEvalPrompt struct {
	ID     string
	Prompt string
}

// SampleMMLUQuestions contains 10 representative science/math/reasoning questions.
var SampleMMLUQuestions = []MMLUQuestion{
	{
		Question: "What is the capital of France?",
		Choices:  map[string]string{"A": "London", "B": "Berlin", "C": "Paris", "D": "Madrid"},
		Answer:   "C",
	},
	{
		Question: "What is 2 + 2?",
		Choices:  map[string]string{"A": "3", "B": "5", "C": "6", "D": "4"},
		Answer:   "D",
	},
	{
		Question: "Which planet is closest to the Sun?",
		Choices:  map[string]string{"A": "Venus", "B": "Mars", "C": "Earth", "D": "Mercury"},
		Answer:   "D",
	},
	{
		Question: "What is the chemical symbol for water?",
		Choices:  map[string]string{"A": "O2", "B": "H2O", "C": "CO2", "D": "NaCl"},
		Answer:   "B",
	},
	{
		Question: "What is the square root of 144?",
		Choices:  map[string]string{"A": "10", "B": "14", "C": "12", "D": "11"},
		Answer:   "C",
	},
	{
		Question: "Which of the following is a prime number?",
		Choices:  map[string]string{"A": "9", "B": "15", "C": "21", "D": "17"},
		Answer:   "D",
	},
	{
		Question: "What is Newton's second law of motion?",
		Choices:  map[string]string{"A": "F=mc²", "B": "F=ma", "C": "E=mc²", "D": "a=m/F"},
		Answer:   "B",
	},
	{
		Question: "What is the powerhouse of the cell?",
		Choices:  map[string]string{"A": "Nucleus", "B": "Ribosome", "C": "Mitochondria", "D": "Golgi apparatus"},
		Answer:   "C",
	},
	{
		Question: "In Python, which keyword is used to define a function?",
		Choices:  map[string]string{"A": "func", "B": "function", "C": "define", "D": "def"},
		Answer:   "D",
	},
	{
		Question: "What does CPU stand for?",
		Choices:  map[string]string{"A": "Central Processing Unit", "B": "Computer Personal Unit", "C": "Central Program Utility", "D": "Core Processing Unit"},
		Answer:   "A",
	},
}

// SampleHumanEvalPrompts contains 5 function-completion prompts.
var SampleHumanEvalPrompts = []HumanEvalPrompt{
	{
		ID:     "HE-001",
		Prompt: "Write a Python function called `sum_list` that takes a list of numbers and returns their sum.",
	},
	{
		ID:     "HE-002",
		Prompt: "Write a Python function called `is_prime` that checks if a given integer is a prime number and returns True or False.",
	},
	{
		ID:     "HE-003",
		Prompt: "Write a Python function called `reverse_string` that takes a string and returns it reversed.",
	},
	{
		ID:     "HE-004",
		Prompt: "Write a Python function called `fibonacci` that returns the nth Fibonacci number.",
	},
	{
		ID:     "HE-005",
		Prompt: "Write a Python function called `count_vowels` that counts the number of vowels in a given string.",
	},
}
