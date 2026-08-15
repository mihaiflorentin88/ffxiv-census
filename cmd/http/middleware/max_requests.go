package middleware

import (
    "log"
    "math/rand"
    "net/http"
    "os"
    "sync/atomic"
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
                log.Printf("sending %s to pid %d", os.Interrupt, proc.Pid)
                if err := proc.Signal(os.Interrupt); err != nil {
                    log.Printf("failed to send %s to pid %d: %v", os.Interrupt, proc.Pid, err)
                }
            } else {
                log.Printf("cannot send kill signal: %v", err)
            }
        }
        next.ServeHTTP(w, r)
    })
}
