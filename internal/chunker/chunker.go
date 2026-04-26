// Package chunker splits long text into overlapping windows suitable for
// embedding. The chunker works in characters rather than tokens to avoid
// pulling in a tokenizer; for nomic-embed-text the window is generous
// enough that the heuristic ~4 chars/token mapping is fine.
package chunker

import "strings"

// Config controls chunk sizing. Sizes are in characters.
type Config struct {
	Size    int // target chunk size
	Overlap int // overlap between adjacent chunks
}

// Default gives ~500 tokens per chunk with ~50 token overlap (4 chars/token).
func Default() Config {
	return Config{Size: 2000, Overlap: 200}
}

// Chunk splits text into windows of cfg.Size characters with cfg.Overlap
// characters of overlap. Splits prefer paragraph then sentence boundaries
// near the target size to avoid cutting sentences in half.
func Chunk(text string, cfg Config) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if len(text) <= cfg.Size {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(text) {
		end := start + cfg.Size
		if end >= len(text) {
			chunks = append(chunks, strings.TrimSpace(text[start:]))
			break
		}
		// try to back up to a clean boundary within the last 20% of the window
		boundary := findBoundary(text, start, end)
		chunks = append(chunks, strings.TrimSpace(text[start:boundary]))
		// advance with overlap, but always make forward progress
		next := boundary - cfg.Overlap
		if next <= start {
			next = boundary
		}
		start = next
	}
	return chunks
}

// findBoundary scans backwards from `end` looking for a paragraph break,
// then a sentence terminator, falling back to `end` itself. Won't go
// further back than 80% of the window.
func findBoundary(text string, start, end int) int {
	minBoundary := start + (end-start)*8/10
	// prefer paragraph break
	if i := strings.LastIndex(text[minBoundary:end], "\n\n"); i != -1 {
		return minBoundary + i + 2
	}
	// then sentence end followed by space
	for _, sep := range []string{". ", "! ", "? ", ".\n", "!\n", "?\n"} {
		if i := strings.LastIndex(text[minBoundary:end], sep); i != -1 {
			return minBoundary + i + len(sep)
		}
	}
	return end
}
