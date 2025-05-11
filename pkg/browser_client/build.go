//go:build ignore
// +build ignore

// This file is used to generate the minified JavaScript file during build.
// It is not included in the final binary.
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	// Get the current working directory
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting current directory: %v\n", err)
		os.Exit(1)
	}

	// Source and destination files
	srcFile := filepath.Join(dir, "dist", "websocketmq.js")
	destFile := filepath.Join(dir, "dist", "websocketmq.min.js")

	// Check if source file exists
	if _, err := os.Stat(srcFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Source file does not exist: %s\n", srcFile)
		os.Exit(1)
	}

	// Ensure dist directory exists
	distDir := filepath.Join(dir, "dist")
	if _, err := os.Stat(distDir); os.IsNotExist(err) {
		if err := os.MkdirAll(distDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating dist directory: %v\n", err)
			os.Exit(1)
		}
	}

	// Try to use terser if available (better minification)
	terserCmd := exec.Command("npx", "terser", srcFile, "--compress", "--mangle", "--output", destFile)
	terserErr := terserCmd.Run()
	if terserErr == nil {
		fmt.Printf("Successfully minified %s to %s using terser\n", srcFile, destFile)
		return
	}

	// Fallback to uglifyjs if terser is not available
	uglifyCmd := exec.Command("npx", "uglify-js", srcFile, "-o", destFile)
	uglifyErr := uglifyCmd.Run()
	if uglifyErr == nil {
		fmt.Printf("Successfully minified %s to %s using uglify-js\n", srcFile, destFile)
		return
	}

	// If both fail, try to use esbuild (which might be installed via Go modules)
	esbuildCmd := exec.Command("npx", "esbuild", srcFile, "--minify", "--outfile="+destFile)
	esbuildErr := esbuildCmd.Run()
	if esbuildErr == nil {
		fmt.Printf("Successfully minified %s to %s using esbuild\n", srcFile, destFile)
		return
	}

	// If all external tools fail, create a simple copy as fallback
	// (this ensures the build doesn't fail if no minifier is available)
	fmt.Printf("Warning: Could not find minifier tools. Creating a copy instead.\n")
	fmt.Printf("For better minification, install Node.js and run: npm install -g terser\n")

	// Read the source file
	content, err := ioutil.ReadFile(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading source file: %v\n", err)
		os.Exit(1)
	}

	// Write to destination file
	err = ioutil.WriteFile(destFile, content, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing destination file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created a copy of %s to %s (no minification)\n", srcFile, destFile)
}
