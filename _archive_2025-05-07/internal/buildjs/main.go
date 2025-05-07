// internal/buildjs/main.go
package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/js"
)

// Simple program to copy and minify JavaScript files
// Used by go generate to build client files
func main() {
	// Get source and destination directories
	srcDir := "client/src"
	distDir := "dist"

	// Ensure dist directory exists
	if err := os.MkdirAll(distDir, 0755); err != nil {
		log.Fatalf("Failed to create dist directory: %v", err)
	}

	// Process client.js
	srcFile := filepath.Join(srcDir, "client.js")
	unminFile := filepath.Join(distDir, "websocketmq.js")
	minFile := filepath.Join(distDir, "websocketmq.min.js")

	// Copy unminified version
	if err := copyFile(srcFile, unminFile); err != nil {
		log.Fatalf("Failed to copy unminified file: %v", err)
	}
	log.Printf("Copied %s to %s", srcFile, unminFile)

	// Create minified version
	if err := minifyFile(srcFile, minFile); err != nil {
		log.Fatalf("Failed to create minified file: %v", err)
	}
	log.Printf("Minified %s to %s", srcFile, minFile)

	// Print stats
	srcInfo, err := os.Stat(srcFile)
	if err != nil {
		log.Fatalf("Failed to stat source file: %v", err)
	}

	minInfo, err := os.Stat(minFile)
	if err != nil {
		log.Fatalf("Failed to stat minified file: %v", err)
	}

	reduction := float64(srcInfo.Size()-minInfo.Size()) / float64(srcInfo.Size()) * 100
	log.Printf("Size reduction: %.1f%% (from %d bytes to %d bytes)",
		reduction, srcInfo.Size(), minInfo.Size())
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Copy contents
	_, err = io.Copy(dstFile, srcFile)
	return err
}

// minifyFile creates a minified version of a JavaScript file
func minifyFile(src, dst string) error {
	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Create minifier
	m := minify.New()
	m.AddFunc("text/javascript", js.Minify)

	// Minify JavaScript
	reader := srcFile
	mediatype := "text/javascript"
	ext := strings.ToLower(filepath.Ext(src))
	if ext == ".js" {
		mediatype = "text/javascript"
	}

	return m.Minify(mediatype, dstFile, reader)
}