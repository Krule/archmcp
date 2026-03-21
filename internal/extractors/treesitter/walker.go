// Package treesitter provides shared helpers for walking tree-sitter ASTs.
// It reduces duplication across language-specific extractors that use tree-sitter
// for parsing (e.g., TypeScript, future Rust tree-sitter migration).
package treesitter

import (
	"fmt"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// Parse parses source code with the given tree-sitter language and returns the
// resulting tree. The caller must call tree.Close() when done.
func Parse(src []byte, lang *sitter.Language) (*sitter.Tree, error) {
	if lang == nil {
		return nil, fmt.Errorf("treesitter: language must not be nil")
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(lang)

	tree := parser.Parse(src, nil)
	return tree, nil
}

// NodeText returns the source text covered by node.
func NodeText(node *sitter.Node, src []byte) string {
	return string(src[node.StartByte():node.EndByte()])
}

// NodeKindIs returns true if node is non-nil and its Kind() equals kind.
func NodeKindIs(node *sitter.Node, kind string) bool {
	if node == nil {
		return false
	}
	return node.Kind() == kind
}

// FindChildByKind returns the first direct child of node whose Kind() matches
// kind, or nil if no match is found.
func FindChildByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

// FindChildrenByKind returns all direct children of node whose Kind() matches kind.
func FindChildrenByKind(node *sitter.Node, kind string) []*sitter.Node {
	if node == nil {
		return nil
	}
	var result []*sitter.Node
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.Kind() == kind {
			result = append(result, child)
		}
	}
	return result
}

// FindDescendantByKind performs a depth-first search and returns the first
// descendant node (at any depth) whose Kind() matches kind. Returns nil if
// no match is found.
func FindDescendantByKind(node *sitter.Node, kind string) *sitter.Node {
	if node == nil {
		return nil
	}
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.Kind() == kind {
			return child
		}
		if found := FindDescendantByKind(child, kind); found != nil {
			return found
		}
	}
	return nil
}

// WalkTopLevel calls fn for each direct child of node. It does not recurse
// into nested nodes — callers can use it to iterate top-level statements in
// a program/module root.
func WalkTopLevel(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	for i := range node.ChildCount() {
		fn(node.Child(i))
	}
}
