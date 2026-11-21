package effects

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestEffectLifetimeWaitsForRunningWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifetime := &Lifetime{}
		if !lifetime.Begin() {
			t.Fatal("fresh lifetime refused work")
		}
		stopped := make(chan struct{})
		go func() { lifetime.CloseAndWait(time.Minute); close(stopped) }()
		synctest.Wait()
		if lifetime.Begin() {
			t.Fatal("shutdown did not close admission")
		}
		select {
		case <-stopped:
			t.Fatal("closed while work was running")
		default:
		}
		started := time.Now()
		lifetime.Done()
		<-stopped
		if time.Since(started) != 0 {
			t.Fatal("drain waited after running work finished")
		}
		if lifetime.Begin() {
			t.Fatal("closed lifetime admitted work")
		}
	})
}

func TestEffectLifetimeWaitIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		lifetime := &Lifetime{}
		lifetime.Begin()
		defer lifetime.Done()
		if lifetime.CloseAndWait(10 * time.Millisecond) {
			t.Fatal("reported running work as finished")
		}
	})
}
