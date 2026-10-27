package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	docker, err := agent.NewDockerEngine(agent.DefaultAgentRoot, nil)
	if err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
	server, err := agent.NewServerFromFilesWithRuntimeAndDocker(*configPath, nil, runtime, docker)
	if err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ingress := agent.NewIngress()
	if err := ingress.Start(ctx); err != nil {
		log.Fatalf("devstack-agent: start fixed ingress: %v", err)
	}
	defer ingress.Stop()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := ingress.Refresh(ctx); err != nil {
					log.Printf("devstack-agent: refresh ingress gateway: %v", err)
				}
			}
		}
	}()
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatalf("devstack-agent: %v", err)
	}
}
