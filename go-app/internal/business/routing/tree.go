package routing

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// RouteTree represents an immutable routing tree built from RouteConfig.
//
// A RouteTree provides:
// - Fast route lookup via hierarchical structure
// - Parameter inheritance down the tree
// - Tree traversal API (Walk, GetAllReceivers, etc.)
// - Hot reload support (via Clone and atomic swap)
//
// Thread Safety:
// - RouteTree is immutable after construction (Build returns readonly tree)
// - Read operations (GetTree, Walk, GetStats) are thread-safe
// - Write operations (Reload) are serialized via RouteTreeManager
//
// Usage:
//
//	// Build tree from config
//	builder := routing.NewTreeBuilder(config, opts)
//	tree, err := builder.Build()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Use tree for routing
//	tree.Walk(func(node *RouteNode) bool {
//	    fmt.Printf("Route: %s\n", node.Path)
//	    return true
//	})
//
// Hot Reload:
//
//	// Via RouteTreeManager (atomic swap)
//	manager := routing.NewRouteTreeManager(tree)
//	if err := manager.Reload(newConfig); err != nil {
//	    manager.Rollback()
//	}
type RouteTree struct {
	// Root is the root node of the routing tree (always present).
	// Root node typically has no matchers (matches all) and acts as default fallback.
	Root *RouteNode

	// receivers is a map of receiver name → receiver config.
	// Pre-built at tree construction time for O(1) receiver lookup.
	// Used to resolve ReceiverConfig pointers in RouteNode.
	receivers map[string]*Receiver

	// stats contains cached statistics about the tree.
	// Calculated once at tree construction time.
	stats TreeStats

	// built is the timestamp when this tree was constructed.
	// Used for tracking tree age and hot reload debugging.
	built time.Time
}

// TreeStats contains statistics about the routing tree.
//
// Statistics are calculated once at tree build time and cached.
type TreeStats struct {
	// NodeCount is the total number of nodes in the tree (including root).
	NodeCount int

	// MaxDepth is the maximum depth of the tree (root = 0).
	// Depth is the number of edges from root to the deepest leaf.
	MaxDepth int

	// ReceiverCount is the number of unique receivers referenced in the tree.
	// Excludes receivers defined in config but not used in any route.
	ReceiverCount int
}

// GetStats returns cached statistics about the tree.
//
// Complexity: O(1) (cached at build time)
func (t *RouteTree) GetStats() TreeStats {
	return t.stats
}

// GetBuildTime returns the timestamp when this tree was built.
//
// Useful for:
// - Tracking tree age
// - Debugging hot reload issues
// - Logging/monitoring tree lifecycle
func (t *RouteTree) GetBuildTime() time.Time {
	return t.built
}

// GetReceiver returns the receiver configuration for the given name.
//
// Returns nil if receiver not found (should be caught by validation).
//
// Complexity: O(1)
func (t *RouteTree) GetReceiver(name string) *Receiver {
	return t.receivers[name]
}

// HasReceiver returns true if the given receiver exists in this tree.
//
// Complexity: O(1)
func (t *RouteTree) HasReceiver(name string) bool {
	_, exists := t.receivers[name]
	return exists
}

// Walk performs a depth-first traversal of the routing tree.
//
// The visitor function is called for each node in the tree (including root).
// Traversal continues as long as visitor returns true.
// If visitor returns false, traversal stops immediately.
//
// Traversal Order:
// - Depth-first (process node, then recursively process children)
// - Children are visited in the order they appear in config
//
// Use Cases:
// - Find all routes matching certain criteria
// - Collect statistics about the tree
// - Debug tree structure
// - Implement custom validation
//
// Example:
//
//	// Count nodes with continue=true
//	count := 0
//	tree.Walk(func(node *RouteNode) bool {
//	    if node.Continue {
//	        count++
//	    }
//	    return true
//	})
//
// Complexity: O(N) where N is the number of nodes in the tree
func (t *RouteTree) Walk(visitor func(*RouteNode) bool) error {
	if t.Root == nil {
		return fmt.Errorf("empty tree (no root)")
	}

	// Recursive DFS traversal
	var walk func(*RouteNode) bool
	walk = func(node *RouteNode) bool {
		// Visit current node
		if !visitor(node) {
			return false // Stop traversal
		}

		// Visit children
		for _, child := range node.Children {
			if !walk(child) {
				return false
			}
		}

		return true
	}

	walk(t.Root)
	return nil
}

// GetAllReceivers returns a sorted list of all receiver names in this tree.
//
// Returns:
// - All receivers defined in config.receivers (even if not used in routes)
// - Sorted alphabetically
//
// Use Cases:
// - Validation (check receiver references)
// - UI display (list available receivers)
// - Metrics (track receiver count)
//
// Complexity: O(R log R) where R is the number of receivers
func (t *RouteTree) GetAllReceivers() []string {
	receivers := make([]string, 0, len(t.receivers))
	for name := range t.receivers {
		receivers = append(receivers, name)
	}

	// Sort for consistent output
	sort.Strings(receivers)

	return receivers
}

// GetDepth returns the maximum depth of the tree.
//
// Depth is defined as:
// - Root node: depth = 0
// - Direct children of root: depth = 1
// - Grandchildren: depth = 2
// - etc.
//
// Complexity: O(1) (cached at build time)
func (t *RouteTree) GetDepth() int {
	return t.stats.MaxDepth
}

// GetNodeCount returns the total number of nodes in the tree (including root).
//
// Complexity: O(1) (cached at build time)
func (t *RouteTree) GetNodeCount() int {
	return t.stats.NodeCount
}

// Clone creates a deep copy of the entire routing tree.
//
// The cloned tree is completely independent from the original:
// - All nodes are cloned recursively
// - Receiver map is copied (pointers to Receiver are shared, but they are immutable)
// - Statistics are copied
// - Build time is updated to current time
//
// Use Cases:
// - Hot reload (build new tree, validate, then atomically swap)
// - Rollback (keep backup of previous tree)
// - Testing (modify tree without affecting original)
//
// Complexity: O(N) where N is the number of nodes
func (t *RouteTree) Clone() *RouteTree {
	if t == nil {
		return nil
	}

	// Clone receiver map (shallow copy, Receiver pointers are shared but immutable)
	receiversClone := make(map[string]*Receiver, len(t.receivers))
	for name, receiver := range t.receivers {
		receiversClone[name] = receiver
	}

	return &RouteTree{
		Root:      t.Root.Clone(), // Deep clone root node and subtree
		receivers: receiversClone,
		stats:     t.stats,   // Copy struct value
		built:     time.Now(), // Update build time for clone
	}
}

// String returns a human-readable representation of the tree.
//
// Format: "RouteTree{nodes=<count> depth=<depth> receivers=<count>}"
//
// Example output:
//
//	"RouteTree{nodes=47 depth=5 receivers=12}"
func (t *RouteTree) String() string {
	return fmt.Sprintf("RouteTree{nodes=%d depth=%d receivers=%d}",
		t.stats.NodeCount, t.stats.MaxDepth, t.stats.ReceiverCount)
}

// IsEmpty returns true if the tree has no nodes (not even a root).
//
// This should never happen with a valid tree built via TreeBuilder,
// but can occur if tree is manually constructed incorrectly.
func (t *RouteTree) IsEmpty() bool {
	return t.Root == nil
}

// Validate performs comprehensive validation of the tree structure.
//
// Validation checks:
// - No cycles in tree (DFS traversal)
// - All receiver references exist
// - All regex matchers compile successfully
// - All duration values are positive
// - No duplicate matchers on same level
//
// Returns:
// - Empty list if tree is valid
// - List of validation errors with detailed messages and paths
//
// Complexity: O(N + E) where N is nodes, E is edges (parent-child links)
//
// Example:
//
//	errors := tree.Validate()
//	if len(errors) > 0 {
//	    for _, err := range errors {
//	        log.Printf("Validation error: %s at %s", err.Message, err.Path)
//	    }
//	}
func (t *RouteTree) Validate() []TreeValidationError {
	var errors []TreeValidationError

	// 1. Check for cycles (DFS)
	errors = append(errors, t.detectCycles()...)

	// 2. Validate receivers
	errors = append(errors, t.validateReceivers()...)

	// 3. Validate matchers (regex compilation)
	errors = append(errors, t.validateMatchers()...)

	// 4. Check for duplicate matchers on same level
	errors = append(errors, t.checkDuplicateMatchers()...)

	// 5. Validate durations (positive values)
	errors = append(errors, t.validateDurations()...)

	return errors
}

// detectCycles checks for cyclic dependencies in the tree using DFS.
//
// A cycle exists if a node is reachable from itself by following parent-child links.
// This should never happen with trees built via TreeBuilder, but we check anyway.
//
// Algorithm: DFS with visited + stack tracking
// Complexity: O(N + E) where E is edges
func (t *RouteTree) detectCycles() []TreeValidationError {
	var errors []TreeValidationError
	visited := make(map[*RouteNode]bool)
	stack := make(map[*RouteNode]bool)

	var dfs func(*RouteNode)
	dfs = func(node *RouteNode) {
		visited[node] = true
		stack[node] = true

		for _, child := range node.Children {
			if !visited[child] {
				dfs(child)
			} else if stack[child] {
				// Cycle detected!
				errors = append(errors, TreeValidationError{
					Type:    ErrCycle,
					Path:    node.Path,
					Message: fmt.Sprintf("cycle detected: %s -> %s", node.Path, child.Path),
					Field:   "routes",
				})
			}
		}

		stack[node] = false
	}

	if t.Root != nil {
		dfs(t.Root)
	}

	return errors
}

// validateReceivers checks that all receiver references exist.
//
// Complexity: O(N) where N is the number of nodes
func (t *RouteTree) validateReceivers() []TreeValidationError {
	var errors []TreeValidationError

	_ = t.Walk(func(node *RouteNode) bool {
		// Check receiver is not empty
		if node.Receiver == "" {
			errors = append(errors, TreeValidationError{
				Type:    ErrEmptyReceiver,
				Path:    node.Path,
				Message: "receiver name is empty",
				Field:   "receiver",
			})
			return true
		}

		// Check receiver exists in config
		if !t.HasReceiver(node.Receiver) {
			errors = append(errors, TreeValidationError{
				Type:    ErrReceiverNotFound,
				Path:    node.Path,
				Message: fmt.Sprintf("receiver '%s' not found in config", node.Receiver),
				Field:   "receiver",
			})
		}

		return true
	})

	return errors
}

// validateMatchers checks that all regex matchers compile successfully.
//
// Complexity: O(N * M) where N is nodes, M is average matchers per node
func (t *RouteTree) validateMatchers() []TreeValidationError {
	var errors []TreeValidationError

	_ = t.Walk(func(node *RouteNode) bool {
		for _, matcher := range node.Matchers {
			if matcher.IsRegex {
				// Try to compile regex
				if _, err := regexp.Compile(matcher.Value); err != nil {
					errors = append(errors, TreeValidationError{
						Type:    ErrInvalidRegex,
						Path:    node.Path,
						Message: fmt.Sprintf("invalid regex pattern '%s': %v", matcher.Value, err),
						Field:   fmt.Sprintf("match_re[%s]", matcher.Name),
					})
				}
			}
		}
		return true
	})

	return errors
}

// checkDuplicateMatchers checks for duplicate matchers on the same level.
//
// Duplicate matchers can cause ambiguous routing behavior.
//
// Complexity: O(N * C) where C is average children per node
func (t *RouteTree) checkDuplicateMatchers() []TreeValidationError {
	var errors []TreeValidationError

	_ = t.Walk(func(node *RouteNode) bool {
		if len(node.Children) < 2 {
			return true // Skip nodes with 0 or 1 children
		}

		// Build signature map for children
		signatures := make(map[string][]string)
		for _, child := range node.Children {
			sig := child.GetMatcherSignature()
			if sig == "" {
				continue // Skip empty matchers
			}
			signatures[sig] = append(signatures[sig], child.Path)
		}

		// Check for duplicates
		for sig, paths := range signatures {
			if len(paths) > 1 {
				errors = append(errors, TreeValidationError{
					Type: ErrDuplicateMatcher,
					Path: node.Path,
					Message: fmt.Sprintf(
						"duplicate matchers '%s' in children: %v",
						sig,
						paths,
					),
					Field: "routes",
				})
			}
		}

		return true
	})

	return errors
}

// validateDurations checks that all duration values are positive.
//
// Complexity: O(N) where N is nodes
func (t *RouteTree) validateDurations() []TreeValidationError {
	var errors []TreeValidationError

	_ = t.Walk(func(node *RouteNode) bool {
		// Check GroupWait
		if node.GroupWait <= 0 {
			errors = append(errors, TreeValidationError{
				Type:    ErrInvalidDuration,
				Path:    node.Path,
				Message: fmt.Sprintf("group_wait must be positive, got %v", node.GroupWait),
				Field:   "group_wait",
			})
		}

		// Check GroupInterval
		if node.GroupInterval <= 0 {
			errors = append(errors, TreeValidationError{
				Type:    ErrInvalidDuration,
				Path:    node.Path,
				Message: fmt.Sprintf("group_interval must be positive, got %v", node.GroupInterval),
				Field:   "group_interval",
			})
		}

		// Check RepeatInterval
		if node.RepeatInterval <= 0 {
			errors = append(errors, TreeValidationError{
				Type:    ErrInvalidDuration,
				Path:    node.Path,
				Message: fmt.Sprintf("repeat_interval must be positive, got %v", node.RepeatInterval),
				Field:   "repeat_interval",
			})
		}

		// Semantic check: GroupInterval should be >= GroupWait
		if node.GroupInterval < node.GroupWait {
			errors = append(errors, TreeValidationError{
				Type: ErrInvalidDuration,
				Path: node.Path,
				Message: fmt.Sprintf(
					"group_interval (%v) should be >= group_wait (%v)",
					node.GroupInterval,
					node.GroupWait,
				),
				Field: "group_interval",
			})
		}

		return true
	})

	return errors
}
