// Package store wraps the Qdrant gRPC client with the operations our service
// needs: collection bootstrap, chunk upserts, and similarity search.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/qdrant/go-client/qdrant"
)

// Store is a thin facade over qdrant.Client scoped to a single collection.
type Store struct {
	client     *qdrant.Client
	collection string
	dim        uint64
}

// Config configures the connection to Qdrant.
type Config struct {
	Host       string // e.g. "localhost"
	Port       int    // gRPC port, typically 6334
	Collection string // collection name; created if it doesn't exist
	VectorDim  uint64 // embedding dimensionality (768 for nomic-embed-text)
}

// New connects to Qdrant and ensures the collection exists with the right
// vector size. Safe to call repeatedly — existing collections are left alone.
func New(ctx context.Context, cfg Config) (*Store, error) {
	c, err := qdrant.NewClient(&qdrant.Config{
		Host: cfg.Host,
		Port: cfg.Port,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to qdrant: %w", err)
	}

	s := &Store{client: c, collection: cfg.Collection, dim: cfg.VectorDim}
	if err := s.ensureCollection(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases the gRPC connection.
func (s *Store) Close() error {
	return s.client.Close()
}

func (s *Store) ensureCollection(ctx context.Context) error {
	exists, err := s.client.CollectionExists(ctx, s.collection)
	if err != nil {
		return fmt.Errorf("check collection: %w", err)
	}
	if exists {
		return nil
	}
	err = s.client.CreateCollection(ctx, &qdrant.CreateCollection{
		CollectionName: s.collection,
		VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
			Size:     s.dim,
			Distance: qdrant.Distance_Cosine,
		}),
	})
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}
	return nil
}

// Chunk is a piece of a document with its embedding and metadata.
type Chunk struct {
	FileID     string    // shared across all chunks of one upload
	Filename   string    // original uploaded filename
	ChunkIndex int       // 0-based position within the document
	Text       string    // the chunk text itself
	Vector     []float32 // embedding
}

// UpsertChunks writes chunks to Qdrant. Each chunk becomes a single point
// with a UUID id and a payload containing file_id, filename, chunk_index,
// and the chunk text (so search results carry the actual content).
func (s *Store) UpsertChunks(ctx context.Context, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	points := make([]*qdrant.PointStruct, 0, len(chunks))
	for _, ch := range chunks {
		points = append(points, &qdrant.PointStruct{
			Id:      qdrant.NewIDUUID(uuid.NewString()),
			Vectors: qdrant.NewVectors(ch.Vector...),
			Payload: qdrant.NewValueMap(map[string]any{
				"file_id":     ch.FileID,
				"filename":    ch.Filename,
				"chunk_index": ch.ChunkIndex,
				"text":        ch.Text,
			}),
		})
	}
	wait := true
	_, err := s.client.Upsert(ctx, &qdrant.UpsertPoints{
		CollectionName: s.collection,
		Wait:           &wait,
		Points:         points,
	})
	if err != nil {
		return fmt.Errorf("upsert points: %w", err)
	}
	return nil
}

// SearchHit is one similarity-search result.
type SearchHit struct {
	Score      float32
	FileID     string
	Filename   string
	ChunkIndex int64
	Text       string
}

// Search returns the top-k nearest chunks to the query vector.
func (s *Store) Search(ctx context.Context, queryVec []float32, limit uint64) ([]SearchHit, error) {
	res, err := s.client.Query(ctx, &qdrant.QueryPoints{
		CollectionName: s.collection,
		Query:          qdrant.NewQuery(queryVec...),
		Limit:          &limit,
		WithPayload:    qdrant.NewWithPayload(true),
	})
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	hits := make([]SearchHit, 0, len(res))
	for _, p := range res {
		payload := p.GetPayload()
		hits = append(hits, SearchHit{
			Score:      p.GetScore(),
			FileID:     payload["file_id"].GetStringValue(),
			Filename:   payload["filename"].GetStringValue(),
			ChunkIndex: payload["chunk_index"].GetIntegerValue(),
			Text:       payload["text"].GetStringValue(),
		})
	}
	return hits, nil
}
