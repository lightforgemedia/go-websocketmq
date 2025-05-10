# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build/Test Commands
- Build: `go build ./...`
- Run tests: `go test -race ./...`
- Run a single test: `go test -race -run TestName ./path/to/package`
- Run examples: `go run ./examples/simple`
- Generate client JS: `go generate ./...`
- Check code: `go vet ./...`

## Code Style Guidelines

### Go
- Imports: Standard library first, blank line, third-party imports, internal imports
- Names: CamelCase for exported, camelCase for unexported, short names for short-lived vars
- Error handling: Check errors immediately, return early on errors
- Comments: File header comments (`// filename.go`), prefer line-before comments
- Formatting: Use tabs for indentation, compact code style with braces on same line
- Context: Most functions accept `context.Context` as first parameter

### JavaScript
- Use ES6 class syntax, camelCase naming, arrow functions for callbacks
- Use Promises for asynchronous operations
- Keep client code small and focused on WebSocket messaging
- NO MOCKING. DO NOT MOCK ANYTHING. DO NOT SIMULATE ANYTHING. ALL TESTS MUST RUN AGAINST REAL CODE.
- ALWAYS LOOK FOR THE ROOT CAUSE OF THE PROBLEM. DO NOT SIMPLY FIX THE SYMPTOMS.
- DO NOT USE PORT 8080.