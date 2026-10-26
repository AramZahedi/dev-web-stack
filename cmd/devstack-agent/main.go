package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/AramZahedi/dev-web-stack/internal/agent"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "/data/devstack/agent.yaml", "root-owned agent configuration file")
	flag.Parse()

	server, err := agent.NewServerFromFiles(*configPath, nil)
	if err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
}
