package config

import "os"

type Config struct {
	NatsURL      string
	SQLitePath   string
	GRPCAddr     string
	MetricsAddr  string
	DefaultQueue string
	WorkerConc   string
}

func getenv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func Load() Config {
	return Config{
		NatsURL:      getenv("JETQUEUE_NATS_URL", "nats://localhost:4222"),
		SQLitePath:   getenv("JETQUEUE_SQLITE_PATH", "jetqueue.db"),
		GRPCAddr:     getenv("JETQUEUE_GRPC_ADDR", "0.0.0.0:50051"),
		MetricsAddr:  getenv("JETQUEUE_METRICS_ADDR", "0.0.0.0:2112"),
		DefaultQueue: getenv("JETQUEUE_QUEUE_DEFAULT", "default"),
		WorkerConc:   getenv("JETQUEUE_WORKER_CONCURRENCY", "4"),
	}
}
