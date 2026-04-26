// Command server starts the embedserver HTTP API.
//
// Configuration via environment variables:
//
//	HTTP_ADDR        listen address (default ":8080")
//	OLLAMA_URL       Ollama base URL (default "http://localhost:11434")
//	OLLAMA_MODEL     embedding model (default "nomic-embed-text")
//	QDRANT_HOST      Qdrant gRPC host (default "localhost")
//	QDRANT_PORT      Qdrant gRPC port (default 6334)
//	QDRANT_COLLECTION collection name (default "documents")
//	VECTOR_DIM       embedding dimension (default 768 for nomic-embed-text)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/riyadennis/embedserver/internal/chunker"
	"github.com/riyadennis/embedserver/internal/embedder"
	"github.com/riyadennis/embedserver/internal/server"
	"github.com/riyadennis/embedserver/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg := loadConfig()
	logger.Info("starting embedserver",
		"addr", cfg.HTTPAddr,
		"ollama", cfg.OllamaURL,
		"model", cfg.OllamaModel,
		"qdrant", cfg.QdrantHost+":"+strconv.Itoa(cfg.QdrantPort),
		"collection", cfg.QdrantCollection)

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	st, err := store.New(ctx, store.Config{
		Host:       cfg.QdrantHost,
		Port:       cfg.QdrantPort,
		Collection: cfg.QdrantCollection,
		VectorDim:  cfg.VectorDim,
	})
	if err != nil {
		logger.Error("connect to qdrant", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := &server.Server{
		Embedder:  embedder.New(cfg.OllamaURL, cfg.OllamaModel),
		Store:     st,
		ChunkConf: chunker.Default(),
		Logger:    logger,
	}

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

type config struct {
	HTTPAddr         string
	OllamaURL        string
	OllamaModel      string
	QdrantHost       string
	QdrantPort       int
	QdrantCollection string
	VectorDim        uint64
}

func loadConfig() config {
	return config{
		HTTPAddr:         envOr("HTTP_ADDR", ":8080"),
		OllamaURL:        envOr("OLLAMA_URL", "http://localhost:11434"),
		OllamaModel:      envOr("OLLAMA_MODEL", "nomic-embed-text"),
		QdrantHost:       envOr("QDRANT_HOST", "localhost"),
		QdrantPort:       envOrInt("QDRANT_PORT", 6334),
		QdrantCollection: envOr("QDRANT_COLLECTION", "documents"),
		VectorDim:        uint64(envOrInt("VECTOR_DIM", 768)),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
