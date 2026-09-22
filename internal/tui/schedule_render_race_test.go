package tui

import (
	"sync"
	"testing"
	"time"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/provider"
)

// TestScheduleRenderTrailingRefreshStaysOnTeaLoop exercises the throttled
// scheduleRender path while a simulated Bubble Tea loop mutates status line
// state. The trailing time.AfterFunc callback must not touch statusLine*
// fields directly; it may only send renderRequestMsg so the refresh runs on
// the tea loop. Run with -race: before the fix the timer goroutine raced
// with the loop inside requestStatusLineRefresh/startStatusLineRequest.
func TestScheduleRenderTrailingRefreshStaysOnTeaLoop(t *testing.T) {
	a := NewApp(nil, &provider.Model{Name: "test"}, config.DefaultSettings(), nil, nil, "", "", "", nil, "agent", false, false, nil, nil, nil)
	a.width = 80
	a.ready = true
	a.settings.StatusLine.Enabled = true
	a.settings.StatusLine.Command = "ccstatusline"
	a.renderInterval = time.Millisecond

	// Keep the external request marked in-flight so requestStatusLineRefresh
	// mutates statusLinePending instead of spawning command processes.
	a.statusLineInFlight = true

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // simulated tea loop: one goroutine owning App state
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Event burst: the first call renders immediately, the rest land
			// inside the throttle window and arm the trailing timer callback.
			a.scheduleRender()
			a.requestStatusLineRefresh(true)
		}
	}()

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}
