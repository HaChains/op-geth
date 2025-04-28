package sig

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestKill(t *testing.T) {
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT)
		sig := <-sigChan
		t.Log("sig received:", sig.String())
		wg.Done()
	}()
	time.Sleep(time.Second)
	Int("test")
	wg.Wait()
}
