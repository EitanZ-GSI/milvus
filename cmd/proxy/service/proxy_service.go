package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/zilliztech/milvus-distributed/cmd/distributed/components"

	"go.uber.org/zap"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := components.NewProxyService(ctx)
	if err != nil {
		log.Fatal("create proxy service error: " + err.Error())
	}
	sc := make(chan os.Signal, 1)
	signal.Notify(sc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	var sig os.Signal
	go func() {
		sig = <-sc
		log.Println("receive stop signal")
		cancel()
	}()

	if err := s.Run(); err != nil {
		log.Fatal("init server failed", zap.Error(err))
	}

	<-ctx.Done()
	log.Print("Got signal to exit", zap.String("signal", sig.String()))

	if err := s.Stop(); err != nil {
		log.Fatal("stop server failed", zap.Error(err))
	}
	switch sig {
	case syscall.SIGTERM:
		exit(0)
	default:
		exit(1)
	}
}

func exit(code int) {
	os.Exit(code)
}