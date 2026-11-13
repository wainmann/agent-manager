package value

import (
	"slices"
	"testing"
)

func TestMatch(t *testing.T) {
	// The shape Claude on Bedrock answers with.
	bedrock := []Model{
		{ID: "default", Label: "Default"},
		{ID: "us.anthropic.claude-sonnet-5-5", Label: "Sonnet"},
		{ID: "us.anthropic.claude-sonnet-5", Label: "Sonnet 5"},
		{ID: "opus", Label: "Opus"},
		{ID: "haiku", Label: "Haiku"},
	}
	routed := []Model{
		{ID: "gpt", Provider: "a", Label: "GPT"},
		{ID: "gpt", Provider: "b", Label: "GPT"},
		{ID: "x", Provider: "a", Label: "Sonnet"},
		{ID: "sonnet", Provider: "b"},
	}
	for _, tc := range []struct {
		models []Model
		key    string
		want   []string
	}{
		{bedrock, "sonnet", []string{"us.anthropic.claude-sonnet-5-5"}},
		{bedrock, "us.anthropic.claude-sonnet-5", []string{"us.anthropic.claude-sonnet-5"}},
		{bedrock, "opus", []string{"opus"}},
		{bedrock, "fable", nil},
		{routed, "b:gpt", []string{"b:gpt"}},
		{routed, "gpt", []string{"a:gpt", "b:gpt"}},
		// An id wins over another model's label.
		{routed, "sonnet", []string{"b:sonnet"}},
		{routed, "SONNET", []string{"a:x"}},
	} {
		var got []string
		for _, model := range Match(tc.models, tc.key) {
			got = append(got, model.Key())
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("Match(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}
