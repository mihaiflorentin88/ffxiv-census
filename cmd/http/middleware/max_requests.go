package middleware

import (
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"sync/atomic"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
)

type MaxReqMiddleware struct {
	max     uint64
	counter uint64
}

func NewMaxReqMiddleware(max uint64) *MaxReqMiddleware {
	if max != 0 {
		jitter := uint64(rand.Intn(1000) + 1)
		max += jitter
	}
	return &MaxReqMiddleware{max: max}
}

func (m *MaxReqMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.max == 0 {
			next.ServeHTTP(w, r)
			return
		}
		count := atomic.AddUint64(&m.counter, 1)
		if count >= m.max {
			proc, err := os.FindProcess(os.Getpid())
			if err == nil {
				logging.Info("Sending shutdown signal", fmt.Sprintf("signal=%s pid=%d", os.Interrupt, proc.Pid))
				if err := proc.Signal(os.Interrupt); err != nil {
					logging.Error("Failed to send shutdown signal", fmt.Sprintf("signal=%s pid=%d error=%v", os.Interrupt, proc.Pid, err))
				}
			} else {
				logging.Error("Cannot find process for shutdown", fmt.Sprintf("error=%v", err))
			}
		}
		next.ServeHTTP(w, r)
	})
}
