package main

import (
	"log"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hruturajbabar/jetqueue/internal/config"
	"github.com/hruturajbabar/jetqueue/internal/metrics"
	"github.com/hruturajbabar/jetqueue/internal/queue"
	"github.com/hruturajbabar/jetqueue/internal/store"
)

func main() {
	cfg := config.Load()

	if _, err := strconv.Atoi(cfg.WorkerConc); err != nil {
		log.Fatalf("bad JETQUEUE_WORKER_CONCURRENCY=%q: %v", cfg.WorkerConc, err)
	}

	st, err := store.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatal(err)
	}
	_ = st

	q, err := queue.Connect(cfg.NatsURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := q.EnsureStream(); err != nil {
		log.Fatal(err)
	}

	reg := prometheus.NewRegistry()
	_ = metrics.New(reg)
	go serveMetrics("worker", cfg.MetricsAddr, reg)

	log.Printf("worker: connected (stub). Next: consume JetStream + process jobs.")
	select {}
}

func serveMetrics(name, addr string, reg *prometheus.Registry) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	log.Printf("%s: metrics on %s/metrics", name, addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
