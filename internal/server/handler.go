// Package server wires the upload + search HTTP endpoints to the extractor,
// chunker, embedder, and store.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
	"github.com/riyadennis/embedserver/internal/chunker"
	"github.com/riyadennis/embedserver/internal/embedder"
	"github.com/riyadennis/embedserver/internal/extractor"
	"github.com/riyadennis/embedserver/internal/store"
)

// maxUploadBytes caps multipart uploads at 50 MB. Tune for your use case.
const maxUploadBytes = 50 << 20

// Server holds the dependencies the handlers need.
type Server struct {
	Embedder  *embedder.Client
	Store     *store.Store
	ChunkConf chunker.Config
	Logger    *slog.Logger
}

// Routes returns an http.Handler with all endpoints registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /upload", s.handleUpload)
	mux.HandleFunc("POST /search", s.handleSearch)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// uploadResponse is what /upload returns on success.
type uploadResponse struct {
	FileID   string `json:"file_id"`
	Filename string `json:"filename"`
	Chunks   int    `json:"chunks"`
}

// handleUpload accepts a multipart form with a single "file" field, extracts
// text, chunks it, embeds each chunk, and stores all chunks in Qdrant under
// a shared file_id.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("parse multipart: %v", err))
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' form field")
		return
	}
	defer file.Close()

	// Persist to a temp file so the extractor (which works on paths) can
	// read it. We copy from the multipart reader to avoid loading the
	// whole upload into memory.
	tmp, err := os.CreateTemp("", "upload-*"+filepath.Ext(header.Filename))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create temp file")
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := io.Copy(tmp, file); err != nil {
		writeError(w, http.StatusInternalServerError, "save upload")
		return
	}
	if err := tmp.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, "close temp file")
		return
	}

	// Extract.
	text, err := extractor.Extract(tmp.Name())
	if err != nil {
		if errors.Is(err, extractor.ErrUnsupported) {
			writeError(w, http.StatusUnsupportedMediaType, err.Error())
			return
		}
		s.Logger.Error("extract failed", "filename", header.Filename, "err", err)
		writeError(w, http.StatusInternalServerError, "extract: "+err.Error())
		return
	}
	if text == "" {
		writeError(w, http.StatusBadRequest, "no text extracted from file")
		return
	}

	// Chunk.
	chunks := chunker.Chunk(text, s.ChunkConf)
	if len(chunks) == 0 {
		writeError(w, http.StatusBadRequest, "file produced no chunks")
		return
	}

	// Embed all chunks.
	vectors, err := s.Embedder.EmbedBatch(r.Context(), chunks)
	if err != nil {
		s.Logger.Error("embed failed", "filename", header.Filename, "err", err)
		writeError(w, http.StatusBadGateway, "embed: "+err.Error())
		return
	}

	// Build store records sharing one file_id.
	fileID := uuid.NewString()
	records := make([]store.Chunk, len(chunks))
	for i, c := range chunks {
		records[i] = store.Chunk{
			FileID:     fileID,
			Filename:   header.Filename,
			ChunkIndex: i,
			Text:       c,
			Vector:     vectors[i],
		}
	}

	if err := s.Store.UpsertChunks(r.Context(), records); err != nil {
		s.Logger.Error("upsert failed", "filename", header.Filename, "err", err)
		writeError(w, http.StatusInternalServerError, "store: "+err.Error())
		return
	}

	s.Logger.Info("upload complete",
		"filename", header.Filename,
		"file_id", fileID,
		"chunks", len(chunks))

	writeJSON(w, http.StatusOK, uploadResponse{
		FileID:   fileID,
		Filename: header.Filename,
		Chunks:   len(chunks),
	})
}

// searchRequest is the JSON body for /search.
type searchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// handleSearch embeds the query string and returns the top-k nearest chunks.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode body: "+err.Error())
		return
	}
	if req.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	if req.Limit <= 0 {
		req.Limit = 5
	}

	vec, err := s.Embedder.Embed(r.Context(), req.Query)
	if err != nil {
		writeError(w, http.StatusBadGateway, "embed query: "+err.Error())
		return
	}

	hits, err := s.Store.Search(r.Context(), vec, uint64(req.Limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query":   req.Query,
		"results": hits,
	})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// PortFromEnv reads a port from env with a fallback.
func PortFromEnv(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if _, err := strconv.Atoi(v); err != nil {
		return fallback
	}
	return v
}
