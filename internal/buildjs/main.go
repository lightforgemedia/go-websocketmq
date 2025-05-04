// Package main provides a tool for minifying JavaScript files.
//
// This program is intended to be run via `go generate` to create minified
// versions of the JavaScript client files.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/js"
)

// main is the entry point for the buildjs tool.
//
//go:generate go run .
func main() {
	// Get the root directory of the project
	rootDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current directory: %v\n", err)
		os.Exit(1)
	}

	// Source and destination paths
	srcPath := filepath.Join(rootDir, "client/src/client.js")
	distDir := filepath.Join(rootDir, "dist")
	fullJSPath := filepath.Join(distDir, "websocketmq.js")
	minJSPath := filepath.Join(distDir, "websocketmq.min.js")

	// Ensure dist directory exists
	if err := os.MkdirAll(distDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating dist directory: %v\n", err)
		os.Exit(1)
	}

	// Read the source file
	srcContent, err := os.ReadFile(srcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading source file: %v\n", err)
		os.Exit(1)
	}

	// Write the full JS file
	if err := os.WriteFile(fullJSPath, srcContent, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing full JS file: %v\n", err)
		os.Exit(1)
	}

	// Minify the JS file
	m := minify.New()
	m.AddFunc("application/javascript", js.Minify)

	minified, err := m.Bytes("application/javascript", srcContent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error minifying JS: %v\n", err)
		os.Exit(1)
	}

	// Write the minified JS file
	if err := os.WriteFile(minJSPath, minified, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing minified JS file: %v\n", err)
		os.Exit(1)
	}

	// Print success message
	srcSize := len(srcContent)
	minSize := len(minified)
	reduction := 100 - (minSize * 100 / srcSize)

	fmt.Printf("JavaScript build complete:\n")
	fmt.Printf("  Source:   %s (%d bytes)\n", srcPath, srcSize)
	fmt.Printf("  Full:     %s (%d bytes)\n", fullJSPath, srcSize)
	fmt.Printf("  Minified: %s (%d bytes, %d%% reduction)\n", minJSPath, minSize, reduction)
}
