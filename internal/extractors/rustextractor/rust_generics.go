package rustextractor

import (
	"github.com/dejo1307/archmcp/internal/extractors/treesitter"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

// extractTypeParams extracts type parameters, lifetime parameters, and trait bounds
// from a node that may contain a type_parameters child (e.g., function_item, struct_item,
// impl_item, trait_item, enum_item).
// Returns:
//   - typeParams: type parameter names (e.g., ["T", "U"])
//   - lifetimes: lifetime names (e.g., ["'a", "'b"])
//   - bounds: trait bounds per type param (e.g., {"T": ["Clone", "Debug"]})
func extractTypeParams(node *sitter.Node, src []byte) (typeParams []string, lifetimes []string, bounds map[string][]string) {
	if node == nil {
		return nil, nil, nil
	}

	tp := treesitter.FindChildByKind(node, "type_parameters")
	if tp == nil {
		return nil, nil, nil
	}

	for i := range tp.ChildCount() {
		child := tp.Child(i)
		kind := child.Kind()

		switch kind {
		case "type_parameter":
			name := extractTypeParamName(child, src)
			if name != "" {
				typeParams = append(typeParams, name)
			}
			paramBounds := extractTraitBounds(child, src)
			if len(paramBounds) > 0 {
				if bounds == nil {
					bounds = make(map[string][]string)
				}
				bounds[name] = paramBounds
			}

		case "const_parameter":
			// const generics: `const N: usize` — extract the identifier name
			id := treesitter.FindChildByKind(child, "identifier")
			if id != nil {
				typeParams = append(typeParams, treesitter.NodeText(id, src))
			}

		case "lifetime_parameter":
			lt := treesitter.FindChildByKind(child, "lifetime")
			if lt != nil {
				lifetimes = append(lifetimes, treesitter.NodeText(lt, src))
			}
		}
	}

	if len(typeParams) == 0 {
		typeParams = nil
	}
	if len(lifetimes) == 0 {
		lifetimes = nil
	}

	return typeParams, lifetimes, bounds
}

// extractTypeParamName returns the type_identifier text from a type_parameter node.
func extractTypeParamName(node *sitter.Node, src []byte) string {
	ti := treesitter.FindChildByKind(node, "type_identifier")
	if ti == nil {
		return ""
	}
	return treesitter.NodeText(ti, src)
}

// extractTraitBounds extracts bound names from a type_parameter's trait_bounds child.
// For simple bounds like `T: Clone + Debug`, returns ["Clone", "Debug"].
// For complex bounds like `T: Iterator<Item = u32>`, returns the full text
// (e.g., ["Iterator<Item = u32>"]).
func extractTraitBounds(node *sitter.Node, src []byte) []string {
	tb := treesitter.FindChildByKind(node, "trait_bounds")
	if tb == nil {
		return nil
	}

	var result []string
	for i := range tb.ChildCount() {
		child := tb.Child(i)
		kind := child.Kind()

		switch kind {
		case "type_identifier":
			result = append(result, treesitter.NodeText(child, src))
		case "generic_type":
			// e.g., Iterator<Item = u32> — use full text
			result = append(result, treesitter.NodeText(child, src))
		case "scoped_type_identifier":
			result = append(result, treesitter.NodeText(child, src))
		case "lifetime":
			// e.g., T: 'a + Clone — lifetime as a bound
			result = append(result, treesitter.NodeText(child, src))
		case "function_type":
			// e.g., Fn(u32) -> bool — function trait bound
			result = append(result, treesitter.NodeText(child, src))
		}
	}
	return result
}

// extractWhereClause extracts the where clause text from a node that may contain one.
// Returns the raw where clause string, or "" if none present.
func extractWhereClause(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}

	wc := treesitter.FindChildByKind(node, "where_clause")
	if wc == nil {
		return ""
	}
	return treesitter.NodeText(wc, src)
}

// extractReturnType extracts the return type from a function item node.
// It looks for the "->" token and returns the text of the following sibling node.
// Returns the type string, or "" if the function returns unit/nothing.
func extractReturnType(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}

	// Walk children looking for "->" token; the next meaningful sibling is the return type.
	for i := range node.ChildCount() {
		child := node.Child(i)
		if child.Kind() == "->" {
			// Next sibling is the return type node
			next := i + 1
			if next < node.ChildCount() {
				retNode := node.Child(next)
				return treesitter.NodeText(retNode, src)
			}
		}
	}
	return ""
}
