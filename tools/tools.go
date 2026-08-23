//go:build tools

// Package tools pins build-time dependencies outside the production module.
package tools

// Tool modules are explicit requirements in go.mod. They are commands and cannot
// be imported as ordinary packages; Makefile targets execute the pinned versions.
