package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/YoanWai/agent-manager/internal/ui/composer"
	uiquicksend "github.com/YoanWai/agent-manager/internal/ui/quick/send"
	uilaunch "github.com/YoanWai/agent-manager/internal/ui/spawn/launch"
	tea "github.com/charmbracelet/bubbletea"
)

func TestQuickEnterClearsBeforeTypingNextPrompt(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "target", t.TempDir(), "")
	m.selectSessionRow(t, "target")
	m.openQuickMode()
	m.handleQuickKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("first")})
	_, command := m.handleQuickKey(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("Enter did not accept the first prompt")
	}
	m.handleQuickKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("second")})
	if got := m.quick().Input.Value(); got != "second" {
		t.Fatalf("typing after Enter glued prompts together, got %q", got)
	}
}

func TestQuickAltEnterSendsAndKeepsTheText(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "target", t.TempDir(), "")
	m.selectSessionRow(t, "target")
	m.openQuickMode()
	var delivered []string
	m.services.quickSessionExists = func(*tmux.Driver, string) (bool, error) { return true, nil }
	m.services.quickSendText = func(_ *tmux.Driver, _ string, text string) (tmux.SendResult, error) {
		delivered = append(delivered, text)
		return tmux.SendResult{Phase: tmux.SendPhaseSubmitted}, nil
	}
	m.quick().Input.SetValue("run the tests")
	for range 2 {
		m.handleQuickKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
		if got := m.quick().Input.Value(); got != "run the tests" {
			t.Fatalf("alt+enter cleared the bar, got %q", got)
		}
	}
	for m.effects.Main.Active != nil {
		m.applyTestMsg(t, m.effects.Main.Active.Command())
	}
	if len(delivered) != 2 || delivered[0] != "run the tests" || delivered[1] != "run the tests" {
		t.Fatalf("delivered %q, want the prompt twice", delivered)
	}
	if m.quick() == nil || m.quick().Input.Value() != "run the tests" {
		t.Fatal("the delivered sends took the kept text off the bar")
	}
}

func TestQuickAltEnterRefusalLeavesTheKeptTextOnce(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "target", t.TempDir(), "")
	m.selectSessionRow(t, "target")
	m.openQuickMode()
	q := m.quick()
	q.Input.SetValue("run the tests")
	sent := q.Submission()
	m.handleQuickKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	quickSendRoot{m}.ReturnRefused(uiquicksend.SendRequest{ComposerGen: q.Gen, Submission: sent})
	if got := q.Input.Value(); got != "run the tests" {
		t.Fatalf("refused alt+enter send left the bar as %q", got)
	}
	q.Input.SetValue("newer")
	quickSendRoot{m}.ReturnRefused(uiquicksend.SendRequest{ComposerGen: q.Gen, Submission: sent})
	if got := q.Input.Value(); got != "newer" {
		t.Fatalf("refused alt+enter send replaced a newer prompt, bar = %q", got)
	}
}

func TestQuickAltEnterKeepsTheImageUnderItsOwnName(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "target", t.TempDir(), "")
	m.selectSessionRow(t, "target")
	m.openQuickMode()
	q := m.quick()
	imagePath := filepath.Join(t.TempDir(), "paste-shot.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	q.Input.SetValue("look " + composer.Token(1))
	q.Attachments = []composer.Attachment{{ID: 1, Path: imagePath}}
	sent := q.Submission()
	m.handleQuickKey(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if len(q.Attachments) != 1 || q.Attachments[0].Path == imagePath {
		t.Fatalf("kept chip shares the sent image or lost it: %v", q.Attachments)
	}
	kept := q.Attachments[0].Path
	cleanup := quickSendRoot{m}.ReturnRefused(uiquicksend.SendRequest{
		ComposerGen: q.Gen,
		Submission:  sent,
		Images:      []composer.Attachment{{ID: 1, Path: imagePath}},
	})
	if cleanup != nil {
		cleanup()
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("refused send kept its image, stat err = %v", err)
	}
	if data, err := os.ReadFile(kept); err != nil || string(data) != "png" {
		t.Fatalf("kept chip lost its image: %q, %v", data, err)
	}
	if got := q.Message(); got != "look "+kept {
		t.Fatalf("kept prompt reads %q", got)
	}
}

func TestQuickEnterAcceptsBurstOnSameBar(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "target", t.TempDir(), "")
	m.selectSessionRow(t, "target")
	m.openQuickMode()
	var delivered []string
	m.services.quickSessionExists = func(*tmux.Driver, string) (bool, error) { return true, nil }
	m.services.quickSendText = func(_ *tmux.Driver, _ string, text string) (tmux.SendResult, error) {
		delivered = append(delivered, text)
		return tmux.SendResult{Phase: tmux.SendPhaseSubmitted}, nil
	}
	var first tea.Cmd
	for index := range 5 {
		m.quick().Input.SetValue(fmt.Sprintf("burst%d", index+1))
		_, command := m.handleQuickKey(tea.KeyMsg{Type: tea.KeyEnter})
		if index == 0 {
			first = command
		}
		if m.errBar.text != "" {
			t.Fatalf("Enter %d was refused, got %q", index+1, m.errBar.text)
		}
	}
	if got := len(m.effects.Main.Pending); got != 4 {
		t.Fatalf("queued %d later prompts, want 4", got)
	}
	m.applyTestMsg(t, first())
	if m.quick() == nil {
		t.Fatal("first completion closed the bar after later submissions")
	}
	for m.effects.Main.Active != nil {
		m.applyTestMsg(t, m.effects.Main.Active.Command())
	}
	for index := range 5 {
		want := fmt.Sprintf("burst%d", index+1)
		if len(delivered) != 5 || delivered[index] != want {
			t.Fatalf("delivered %q, want five distinct prompts in order", delivered)
		}
	}
	if m.quick() == nil || m.quick().Input.Value() != "" {
		t.Fatal("the bar should stay open and empty after the burst")
	}
}

func TestQuickRefusedImagesStayOffANewerPrompt(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "target", t.TempDir(), "")
	m.selectSessionRow(t, "target")
	m.openQuickMode()
	q := m.quick()
	imagePath := filepath.Join(t.TempDir(), "first.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	images := []composer.Attachment{{ID: 1, Path: imagePath}}
	q.Input.SetValue("first " + composer.Token(1))
	q.Attachments = images
	first := q.Submission()
	q.Accept(first)
	q.Input.SetValue("second")
	second := q.Submission()
	q.Accept(second)
	root := quickSendRoot{m}
	cleanup := root.ReturnRefused(uiquicksend.SendRequest{ComposerGen: q.Gen, Submission: first, Images: images})
	if len(q.Attachments) != 0 {
		t.Fatalf("refused images joined the newer prompt: %v", q.Attachments)
	}
	if cleanup == nil {
		t.Fatal("refused images were neither returned nor dropped")
	}
	cleanup()
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("refused image kept on disk, stat err = %v", err)
	}
	root.ReturnRefused(uiquicksend.SendRequest{ComposerGen: q.Gen, Submission: second})
	if got := q.Input.Value(); got != "second" {
		t.Fatalf("newest refused prompt not restored, bar = %q", got)
	}
}

func TestQuickSpawnThenSessionSendKeepsPromptsSeparate(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "target", t.TempDir(), "")
	if err := m.services.store.CreateGroup("spawn-here", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m.applyCmd(t, m.refreshCmd())
	m.selectGroupRow(t, "spawn-here")
	m.openQuickMode()
	m.quick().Input.SetValue("spawned task")
	_, command := m.handleQuickKey(tea.KeyMsg{Type: tea.KeyEnter})
	if command == nil {
		t.Fatal("Enter did not accept the spawn")
	}
	spawnRequest := m.effects.Main.Active.Request.(uilaunch.Request)
	if spawnRequest.Prompt != "spawned task" {
		t.Fatalf("spawn prompt = %q", spawnRequest.Prompt)
	}
	m.selectSessionRow(t, "target")
	m.handleQuickKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("answer task")})
	m.handleQuickKey(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.effects.Main.Pending) != 1 {
		t.Fatalf("queued jobs = %d, want the session send", len(m.effects.Main.Pending))
	}
	sendRequest := m.effects.Main.Pending[0].Request.(uiquicksend.SendRequest)
	if sendRequest.Session.Name != "target" || sendRequest.Text != "answer task" {
		t.Fatalf("session received another target's prompt, name=%q text=%q", sendRequest.Session.Name, sendRequest.Text)
	}
}
