package graph

import (
	"log/slog"

	"github.com/riyadennis/embedserver/internal/chunker"
	"github.com/riyadennis/embedserver/internal/embedder"
	"github.com/riyadennis/embedserver/internal/store"
)

// Resolver holds the dependencies the GraphQL resolvers need.
type Resolver struct {
	Embedder  *embedder.Client
	Store     *store.Store
	ChunkConf chunker.Config
	Logger    *slog.Logger
}
