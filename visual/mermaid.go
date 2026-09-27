package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	mermaidVersion = "12.0.0"
	mermaidURL     = "https://cdn.jsdelivr.net/npm/mermaid@" + mermaidVersion + "/dist/mermaid.min.js"
	mermaidSHA256  = "28fca7ae6ebc7ed7bb63bde63136a74bfef14f296a57e403657eeb8b32836073"
)

// mermaidJS returns the path of mermaid.min.js. LEARN_MERMAID_JS overrides the
// path. Otherwise the file is downloaded once, checked, and cached.
func mermaidJS(ctx context.Context) (string, error) {
	if path := os.Getenv("LEARN_MERMAID_JS"); path != "" {
		return path, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cache, "learn-visual", "mermaid-"+mermaidVersion+".min.js")
	if data, err := os.ReadFile(path); err == nil && sha256Hex(data) == mermaidSHA256 {
		return path, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mermaidURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download mermaid (set LEARN_MERMAID_JS to use a local copy): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download mermaid: %s", resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("download mermaid: %w", err)
	}
	if got := sha256Hex(data); got != mermaidSHA256 {
		return "", fmt.Errorf("download mermaid: sha256 %s, want %s", got, mermaidSHA256)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
