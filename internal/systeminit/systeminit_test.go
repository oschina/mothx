package systeminit

import (
	"strings"
	"testing"
)

func TestCommandIsTheSlashSysteminit(t *testing.T) {
	if Command != "/systeminit" {
		t.Fatalf("Command = %q, want %q", Command, "/systeminit")
	}
}

func TestPromptNonInteractiveOmitsQuestionGuidance(t *testing.T) {
	prompt := Prompt(false, "")
	if !strings.Contains(prompt, baseInstructions) {
		t.Fatal("non-interactive prompt is missing the base instructions")
	}
	if strings.Contains(prompt, "question` tool") {
		t.Fatal("non-interactive prompt must not ask the agent to use the question tool")
	}
	if !strings.HasSuffix(prompt, finalNote) {
		t.Fatal("final note must terminate the prompt")
	}
	if prompt != Prompt(false, "") {
		t.Fatal("Prompt is not deterministic for the same inputs")
	}
}

func TestPromptInteractiveAddsQuestionGuidance(t *testing.T) {
	prompt := Prompt(true, "")
	if !strings.Contains(prompt, interactiveInstructions) {
		t.Fatal("interactive prompt is missing the question-tool guidance")
	}
	if !strings.Contains(prompt, "question` tool") {
		t.Fatal("interactive prompt must tell the agent to use the question tool")
	}
	if !strings.HasSuffix(prompt, finalNote) {
		t.Fatal("final note must terminate the prompt")
	}
}

func TestPromptAppendsTrimmedExtraBeforeFinalNote(t *testing.T) {
	prompt := Prompt(false, "  write AGENTS.md in English  ")
	if !strings.Contains(prompt, "Additional user instructions (follow these closely):\nwrite AGENTS.md in English") {
		t.Fatalf("extra instructions were not appended trimmed: %q", prompt)
	}
	if strings.Index(prompt, "write AGENTS.md in English") > strings.Index(prompt, finalNote) {
		t.Fatal("extra instructions must precede the final note")
	}
}

func TestPromptIgnoresBlankExtra(t *testing.T) {
	if Prompt(false, "   \n\t") != Prompt(false, "") {
		t.Fatal("blank extra instructions must not change the prompt")
	}
	if Prompt(true, " ") != Prompt(true, "") {
		t.Fatal("blank extra instructions must not change the interactive prompt")
	}
}
