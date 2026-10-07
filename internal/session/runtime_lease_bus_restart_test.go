package session

import (
	"net"
	"strconv"
	"testing"
	"time"
)

// TestRuntimeLeaseBusReturnsToStartableStateWhenBindingWithNoHandlers locks the
// fix for the bind/unsubscribe race: when the last handler unsubscribes while
// the listener is still binding, the goroutine must reset started/listening/conn
// before returning. Leaving started=true with a closed socket makes every later
// SubscribeRuntimeLeaseNotifications skip starting a listener, silently
// disabling the advisory bus (and making waitForRuntimeLeaseBusListener lie).
func TestRuntimeLeaseBusReturnsToStartableStateWhenBindingWithNoHandlers(t *testing.T) {
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close()
	t.Setenv("MOTHX_RUNTIME_BUS_PORT", strconv.Itoa(port))

	// Reproduce the exact state the goroutine observes after binding when the
	// last subscriber unsubscribed mid-bind: marked started, no handlers.
	runtimeLeaseBus.Lock()
	runtimeLeaseBus.started = true
	runtimeLeaseBus.handlers = map[uint64]func(RuntimeLeaseNotification){}
	runtimeLeaseBus.conn = nil
	runtimeLeaseBus.listening = false
	runtimeLeaseBus.Unlock()
	t.Cleanup(func() {
		runtimeLeaseBus.Lock()
		runtimeLeaseBus.handlers = map[uint64]func(RuntimeLeaseNotification){}
		runtimeLeaseBus.conn = nil
		runtimeLeaseBus.listening = false
		runtimeLeaseBus.started = false
		runtimeLeaseBus.Unlock()
	})

	go runRuntimeLeaseBus()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		runtimeLeaseBus.Lock()
		started := runtimeLeaseBus.started
		listening := runtimeLeaseBus.listening
		conn := runtimeLeaseBus.conn
		handlers := len(runtimeLeaseBus.handlers)
		runtimeLeaseBus.Unlock()
		if !started && !listening && conn == nil {
			return // returned to a startable state as required
		}
		if started && listening && conn != nil && handlers > 0 {
			t.Fatal("listener kept running with a subscriber that never subscribed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	runtimeLeaseBus.Lock()
	started, listening, conn := runtimeLeaseBus.started, runtimeLeaseBus.listening, runtimeLeaseBus.conn
	handlers := len(runtimeLeaseBus.handlers)
	runtimeLeaseBus.Unlock()
	t.Fatalf("bus stuck: started=%v listening=%v conn!=nil=%v handlers=%d", started, listening, conn != nil, handlers)
}
