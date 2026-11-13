package launch

import (
	"slices"

	uieffects "github.com/YoanWai/agent-manager/internal/ui/effects"
)

// Request is the effect a form, the quick bar or the terminal key queues: it
// names itself, owns the slices it carries, and runs against Services.
func (Request) EffectName() string { return "spawn" }

func (r Request) EffectCopy() uieffects.Request {
	r.DirFallbacks = slices.Clone(r.DirFallbacks)
	r.TerminalDir.Fallbacks = slices.Clone(r.TerminalDir.Fallbacks)
	if r.Plan != nil {
		plan := *r.Plan
		plan.PendingInputs = slices.Clone(plan.PendingInputs)
		r.Plan = &plan
	}
	r.Images = slices.Clone(r.Images)
	r.Submission.Saved = slices.Clone(r.Submission.Saved)
	return r
}

func (r Request) Run(s Services) (uieffects.Result, error) { return Run(s, r) }

func (GroupRequest) EffectName() string { return "group creation" }

func (r GroupRequest) EffectCopy() uieffects.Request {
	r.Fallbacks = slices.Clone(r.Fallbacks)
	return r
}

func (r GroupRequest) Run(s Services) (uieffects.Result, error) { return RunGroup(s, r) }

// ChoiceSaveRequest persists a spawn pick the form and the quick bar read
// back: a CLI's kept choice, its recent models, or the default tool. The
// settings cache already holds the value, so the next form reads it without
// waiting for the write.
type ChoiceSaveRequest struct {
	Key   string
	Value string
	// Generation is the key's save generation when the pick replaced the
	// cache, so a completion can tell whether a newer pick owns the value.
	Generation uint64
}

func (ChoiceSaveRequest) EffectName() string { return "model choice save" }

// ChoiceSaveResult carries the stored value back when the write failed, so
// the completion can undo the optimistic cache entry. Valid says the readback
// succeeded; without it Stored is empty because nothing was read, not because
// the store holds nothing.
type ChoiceSaveResult struct {
	Stored string
	Valid  bool
}

func (r ChoiceSaveRequest) Run(s Services) (uieffects.Result, error) {
	return SaveChoice(s.Store, r.Key, r.Value)
}
