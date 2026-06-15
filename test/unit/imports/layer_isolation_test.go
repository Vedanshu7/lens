// Package imports_test enforces the layer boundary rules of the Lens architecture.
// Run with: go test ./test/unit/imports/
//
// Allowed import graph:
//
//	agent => persistence, discovery, transport, target, observability, store, config
//	discovery => persistence  (only allowlisted cross-interface import)
//	discovery/* => persistence, store/memberlist only
//	All other provider packages => only their own interface package + stdlib + third-party
package imports_test

import (
	"os/exec"
	"strings"
	"testing"
)

const (
	modPrefix     = "github.com/Vedanshu7/lens"
	agentPkg      = modPrefix + "/internal/agent"
	persPkg       = modPrefix + "/internal/persistence"
	storePkg      = modPrefix + "/internal/store"
	memberlistPkg = modPrefix + "/internal/discovery/memberlist"
)

// layers lists the five swappable provider layers.
var layers = []string{
	"discovery",
	"observability",
	"persistence",
	"target",
	"transport",
}

func TestLayerBoundaries(t *testing.T) {
	importGraph := buildImportGraph(t)

	for pkg, imports := range importGraph {
		if !isInternalPkg(pkg) {
			continue
		}

		for _, imp := range imports {
			if !isInternalPkg(imp) {
				continue
			}

			// Rule 1: no provider package may import internal/agent.
			if imp == agentPkg && pkg != agentPkg {
				t.Errorf("violation: %s imports %s\n  fix: use the TransportHost callback pattern or constructor injection instead", pkg, agentPkg)
			}

			// Rule 2: no cross-layer imports except allowlisted ones.
			pkgLayer := layerOf(pkg)
			impLayer := layerOf(imp)
			if pkgLayer == "" || impLayer == "" || pkgLayer == impLayer {
				continue
			}

			// Allowlisted: discovery layer may import persistence (constructor-injected backend).
			if pkgLayer == "discovery" && impLayer == "persistence" {
				continue
			}

			// Allowlisted: discovery/memberlist may import store for shared key constants.
			if pkg == memberlistPkg && imp == storePkg {
				continue
			}

			t.Errorf("violation: %s (layer: %s) imports %s (layer: %s)\n  add to allowlist in this file if intentional", pkg, pkgLayer, imp, impLayer)
		}
	}
}

// TestNoProviderImportsAgent is a focused subset of TestLayerBoundaries
// that only checks the most critical rule: providers must never import the agent.
func TestNoProviderImportsAgent(t *testing.T) {
	importGraph := buildImportGraph(t)
	for pkg, imports := range importGraph {
		if !isProviderPkg(pkg) {
			continue
		}
		for _, imp := range imports {
			if imp == agentPkg {
				t.Errorf("critical violation: provider %s imports %s", pkg, agentPkg)
			}
		}
	}
}

// buildImportGraph runs go list and returns a map of pkg → direct internal imports.
func buildImportGraph(t *testing.T) map[string][]string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}: {{join .Imports \" \"}}", "./internal/...")
	cmd.Dir = "../../.." // from test/unit/imports/ to project root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}

	graph := map[string][]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		pkg := parts[0]
		var internal []string
		for _, imp := range strings.Fields(parts[1]) {
			if strings.HasPrefix(imp, modPrefix+"/internal/") {
				internal = append(internal, imp)
			}
		}
		graph[pkg] = internal
	}
	return graph
}

// isInternalPkg returns true for packages under this module's internal/ tree.
func isInternalPkg(pkg string) bool {
	return strings.HasPrefix(pkg, modPrefix+"/internal/")
}

// isProviderPkg returns true for concrete provider packages
// (packages two or more segments deep under internal/).
func isProviderPkg(pkg string) bool {
	rel := strings.TrimPrefix(pkg, modPrefix+"/internal/")
	return strings.Contains(rel, "/")
}

// layerOf returns the swappable layer name for an internal package, or "".
func layerOf(pkg string) string {
	rel := strings.TrimPrefix(pkg, modPrefix+"/internal/")
	top := strings.SplitN(rel, "/", 2)[0]
	for _, l := range layers {
		if top == l {
			return l
		}
	}
	return ""
}
