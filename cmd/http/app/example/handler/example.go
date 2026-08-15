package handler

import (
    "net/http"

    "github.com/mihaiflorentin88/ffxiv-census/container"
    "github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type Handler struct {
    exampleSvc contract.ExampleService
}

func New() Handler {
    if container.Load == nil {
        panic("container not initialised: set container.Load = container.NewServiceContainer() in main()")
    }
    return Handler{exampleSvc: container.Load.ExampleService()}
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/plain; charset=utf-8")
    _, _ = w.Write([]byte(h.exampleSvc.Ping()))
}
