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

	runtime, err := agent.NewRuntime(agent.DefaultAgentRoot)
	if err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
	if err := runtime.Reconcile(context.Background()); err != nil {
		log.Fatalf("devstack-agent: reconcile runtime: %v", err)
	}
	server, err := agent.NewServerFromFilesWithRuntime(*configPath, nil, runtime)
	if err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
}
