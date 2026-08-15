package main

import (
	"log"

	"github.com/mihaiflorentin88/ffxiv-census/cmd/cli"
	"github.com/mihaiflorentin88/ffxiv-census/container"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
)

// main
// @title             ffxiv-census
// @version           1.0.0
func main() {
	container.Load = container.NewServiceContainer()
	initLogging()
	if err := cli.Execute(); err != nil {
		log.Fatal(err)
	}
}

func initLogging() {
	cfg := container.Load.Config().Logging
	if cfg == nil {
		logging.Init(logging.LoggerTypeSimple, "info")
		return
	}
	logging.Init(cfg.Default, cfg.Level)
}
