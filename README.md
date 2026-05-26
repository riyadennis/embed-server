# embedserver

A Go service that accepts file uploads, generates embeddings using a local Ollama model, and stores the chunks in Qdrant for semantic search.

## Architecture

```
POST /upload  ──▶  extract text  ──▶  chunk  ──▶  embed (Ollama)  ──▶  upsert (Qdrant)
POST /search  ──▶  embed query (Ollama)  ──▶  query (Qdrant)  ──▶  ranked chunks
```

Each upload becomes one `file_id` shared across N points in Qdrant — one point per chunk. Payloads carry `file_id`, `filename`, `chunk_index`, and the chunk text so search results are immediately useful.

## Supported file types

`.txt`, `.md`, `.pdf`, `.docx`. Anything else returns `415 Unsupported Media Type`.

## Prerequisites

1. **Ollama** running locally with the embedding model pulled:
   ```bash
   ollama serve              # in one terminal (or as a service)
   ollama pull nomic-embed-text
   ```
2. **Qdrant** running locally:
   ```bash
   docker compose up -d
   ```

## Run

```bash
go mod tidy
go run ./cmd/server
```

The server listens on `:8080`. Override anything via env vars (see `cmd/server/main.go`).

## API

### `POST /upload`
Multipart form, single `file` field.

```bash
curl -F "file=@whitepaper.pdf" http://localhost:8080/upload
```

Response:
```json
{
  "file_id": "8c3f4a72-...",
  "filename": "whitepaper.pdf",
  "chunks": 27
}
```

### `POST /search`
JSON body.

```bash
curl -X POST http://localhost:8080/search \
  -H 'Content-Type: application/json' \
  -d '{"query": "how does the consensus algorithm handle leader election?", "limit": 5}'
```

Response:
```json
{
  "query": "...",
  "results": [
    {
      "Score": 0.81,
      "FileID": "8c3f4a72-...",
      "Filename": "whitepaper.pdf",
      "ChunkIndex": 12,
      "Text": "..."
    }
  ]
}
```

### `GET /healthz`
Liveness check.

### GraphQL

The same upload and search functionality is also available via GraphQL at `/graphql`.

- `GET /graphql` — GraphQL Playground
- `POST /graphql` — queries and mutations

```graphql
query { health }

query {
  search(query: "how does consensus work?", limit: 3) {
    query
    results { score fileID filename chunkIndex text }
  }
}

mutation($file: Upload!) {
  upload(file: $file) { fileID filename chunks }
}
```

### Generate GraphQL docs

Requires [SpectaQL](https://github.com/anvilco/spectaql):

```bash
npx spectaql spectaql.yaml
```

The static HTML documentation is generated into `docs/graphql/`.

## Layout

```
cmd/server/         # entrypoint, config, signal handling
internal/extractor  # txt/md/pdf/docx → string
internal/chunker    # split text into overlapping windows
internal/embedder   # Ollama HTTP client
internal/store      # Qdrant gRPC wrapper
internal/server     # HTTP handlers
```

## Things to improve later

- **Concurrent embedding**: `EmbedBatch` is sequential — fine for small files, slow for big PDFs. A worker pool of 4-8 would help.
- **`.doc` / `.xlsx` / `.pptx`**: not supported. `.docx` only covers modern Word.
- **Better DOCX extraction**: `stripXMLTags` is crude. Walk the XML if you care about table/list structure.
- **Tokenizer-aware chunking**: chunks are sized in characters, not tokens. Good enough for `nomic-embed-text`'s generous window, but a real tokenizer would be more precise.
- **Auth + multi-tenancy**: there's none. Add an API key middleware and a `tenant_id` in payloads if you need to share Qdrant.
- **Idempotent re-uploads**: re-uploading the same file makes new points. Add a content hash and either skip or replace by `file_id`.
