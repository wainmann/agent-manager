package composer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YoanWai/agent-manager/internal/ui/spawn"
)

func TestRefusedSubmissionRestoresSavedPromptForRetry(t *testing.T) {
	prompt := newComposer(SavedToken("Role") + " task")
	prompt.Saved = []spawn.Saved{{Name: "Role", Text: "instructions"}}
	submission := prompt.Submission()
	prompt.Accept(submission)
	prompt.Restore(submission)
	if got := prompt.Message(); got != "instructions task" {
		t.Fatalf("retry prompt = %q", got)
	}
	retry := prompt.Submission()
	if retry.ID == submission.ID {
		t.Fatal("retry reused the earlier submission identity")
	}
}

func TestLateRefusalPreservesNewerPrompt(t *testing.T) {
	for _, state := range []string{"typing", "submitted", "image", "saved prompt"} {
		t.Run(state, func(t *testing.T) {
			prompt := newComposer("first")
			first := prompt.Submission()
			prompt.Accept(first)
			switch state {
			case "typing":
				prompt.Input.SetValue("second")
			case "submitted":
				prompt.Input.SetValue("second")
				prompt.Accept(prompt.Submission())
			case "image":
				prompt.Attachments = []Attachment{{ID: 1, Path: "next.png"}}
			case "saved prompt":
				prompt.Saved = []spawn.Saved{{Name: "Role", Text: "instructions"}}
			}
			before := prompt.Input.Value()
			prompt.Restore(first)
			if got := prompt.Input.Value(); got != before {
				t.Fatalf("late refusal replaced %q with %q", before, got)
			}
		})
	}
}

func TestKeepImagesGivesEachChipItsOwnName(t *testing.T) {
	sent := filepath.Join(t.TempDir(), "paste-1.png")
	if err := os.WriteFile(sent, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompt := newComposer("look " + Token(1) + " and " + Token(2))
	host := &fakeHost{}
	prompt.KeepImages(host, []Attachment{{ID: 1, Path: sent}, {ID: 2, Path: filepath.Join(t.TempDir(), "gone.png")}})
	if len(prompt.Attachments) != 1 || prompt.Attachments[0].ID != 1 || prompt.Attachments[0].Path == sent {
		t.Fatalf("kept attachments = %+v", prompt.Attachments)
	}
	if data, err := os.ReadFile(prompt.Attachments[0].Path); err != nil || string(data) != "png" {
		t.Fatalf("kept image = %q, %v", data, err)
	}
	if len(host.errs) != 1 || strings.Contains(prompt.Input.Value(), Token(2)) {
		t.Fatalf("a missing image left its chip: errs %v, prompt %q", host.errs, prompt.Input.Value())
	}
}
