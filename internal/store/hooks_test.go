package store

import (
	"sync"
	"testing"
	"time"
)

func TestSwapSettingIsOneWinner(t *testing.T) {
	st := newTestStore(t)
	won, err := st.SwapSetting(NotifyCommandMigratedKey, "", "pending")
	if err != nil || !won {
		t.Fatalf("first swap = %v, %v", won, err)
	}
	won, err = st.SwapSetting(NotifyCommandMigratedKey, "", "other")
	if err != nil || won {
		t.Fatalf("second swap = %v, %v", won, err)
	}
	got, err := st.Setting(NotifyCommandMigratedKey)
	if err != nil || got != "pending" {
		t.Fatalf("marker = %q, %v", got, err)
	}
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := st.SwapSetting(NotifyCommandMigratedKey, "pending", "done")
			if err != nil {
				t.Errorf("swap: %v", err)
				return
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("wins = %d, want 1", wins)
	}
}

func TestHookFailuresKeepTheNewestTwenty(t *testing.T) {
	st := newTestStore(t)
	for i := range 25 {
		if err := st.AddHookFailure(HookFailure{At: time.Unix(int64(i), 0).UTC(), Event: "session_state", Command: "echo", Reason: "exit status 1"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.HookFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 || !got[0].At.Equal(time.Unix(5, 0).UTC()) || !got[len(got)-1].At.Equal(time.Unix(24, 0).UTC()) {
		t.Fatalf("kept %d from %v to %v", len(got), got[0].At, got[len(got)-1].At)
	}
}

func TestClaimHookShownOnce(t *testing.T) {
	st := newTestStore(t)
	fresh, err := st.ClaimHookShown("abc")
	if err != nil || !fresh {
		t.Fatalf("first = %v, %v", fresh, err)
	}
	fresh, err = st.ClaimHookShown("abc")
	if err != nil || fresh {
		t.Fatalf("second = %v, %v", fresh, err)
	}
	shown, err := st.HooksShown()
	if err != nil || len(shown) != 1 || shown[0] != "abc" {
		t.Fatalf("shown = %v, %v", shown, err)
	}
}

func TestSpawnedByRoundTrips(t *testing.T) {
	st := newTestStore(t)
	sess := sample("child", "g")
	sess.SpawnedBy = "parent"
	if err := st.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SpawnedBy != "parent" {
		t.Fatalf("get spawned_by = %q", got.SpawnedBy)
	}
	listed, err := st.ListSessions(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].SpawnedBy != "parent" {
		t.Fatalf("list = %+v", listed)
	}
}
