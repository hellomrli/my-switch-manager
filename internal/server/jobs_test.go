package server

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestJobsStartReturnsDetachedOwnSnapshot(t *testing.T) {
	var jobs Jobs
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				title := fmt.Sprintf("%d-%d", n, i)
				j, err := jobs.Start("test", title, func(jc *JobCtx) error {
					for k := 0; k < 8; k++ {
						jc.Progress(float64(k)/8, "stage")
						jc.Summary("updated")
						jc.Logf("log")
					}
					return nil
				})
				if err == ErrBusy {
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				for k := 0; k < 16; k++ {
					if _, err := json.Marshal(j); err != nil {
						t.Error(err)
					}
				}
				if j.Title != title || j.State != "running" || j.Summary != "" || j.Progress != -1 {
					t.Errorf("start snapshot changed or wrong job: %+v", j)
				}
			}
		}(n)
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for jobs.Running() {
		if time.Now().After(deadline) {
			t.Fatal("job timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
