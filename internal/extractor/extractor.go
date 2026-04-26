// Package extractor pulls plain text out of uploaded files based on extension.
//
// Supported types: .txt, .md, .pdf, .docx
// Unsupported types return ErrUnsupported so the HTTP layer can return 415.
package extractor

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/nguyenthenguyen/docx"
)

// ErrUnsupported is returned for file extensions we don't know how to read.
var ErrUnsupported = errors.New("unsupported file type")

// Extract reads the file at path and returns its text content.
// The extension (case-insensitive) determines the extraction strategy.
func Extract(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".md":
		return extractPlain(path)
	case ".pdf":
		return extractPDF(path)
	case ".docx":
		return extractDOCX(path)
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupported, ext)
	}
}

func extractPlain(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read text file: %w", err)
	}
	return string(b), nil
}

func extractPDF(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	reader, err := r.GetPlainText()
	if err != nil {
		return "", fmt.Errorf("read pdf text: %w", err)
	}
	if _, err := io.Copy(&buf, reader); err != nil {
		return "", fmt.Errorf("copy pdf text: %w", err)
	}
	return buf.String(), nil
}

func extractDOCX(path string) (string, error) {
	r, err := docx.ReadDocxFile(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()
	// Editable() exposes the document; GetContent() returns the raw XML body,
	// but docx also gives us a text-only view via the editable wrapper.
	doc := r.Editable()
	// GetContent returns XML; we strip tags crudely. For higher-fidelity
	// extraction, swap in a proper XML walker — fine for a starting point.
	return stripXMLTags(doc.GetContent()), nil
}

// stripXMLTags removes anything between < and >. It's intentionally simple:
// docx body XML is well-formed, so this gives readable plain text without
// pulling in a full XML parser. Replace with encoding/xml walking if you
// need to preserve list/table structure.
func stripXMLTags(s string) string {
	var out bytes.Buffer
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			// emit a space so adjacent runs don't fuse together
			out.WriteByte(' ')
		case !inTag:
			out.WriteRune(r)
		}
	}
	// collapse runs of whitespace
	return strings.Join(strings.Fields(out.String()), " ")
}
