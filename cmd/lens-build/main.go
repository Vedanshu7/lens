// lens-build generates a minimal Lens sidecar binary containing only the
// providers declared in lens.yaml. It reads the config, generates a
// providers_gen.go file in the project root, runs go build, and cleans up.
//
// Usage:
//
//	lens-build [-config lens.yaml] [-output ./lens] [-keep] [-dry-run]
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/format"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Vedanshu7/lens/config"
)

const module = "github.com/Vedanshu7/lens"

// transportImports maps transport provider names to their import paths.
var transportImports = map[string]string{
	"grpc":          module + "/internal/transport/grpc",
	"nats":          module + "/internal/transport/nats",
	"kafka":         module + "/internal/transport/kafka",
	"zeromq":        module + "/internal/transport/zeromq",
	"redis-streams": module + "/internal/transport/redisstreams",
}

// discoveryImports maps discovery provider names to their import paths.
var discoveryImports = map[string]string{
	"memberlist": module + "/internal/discovery/memberlist",
	"nats":       module + "/internal/discovery/nats",
	"dnssrv":     module + "/internal/discovery/dnssrv",
	"static":     module + "/internal/discovery/static",
}

// persistenceImports maps persistence provider names to their import paths.
// redis and memory are always-on, so they have no entries here.
var persistenceImports = map[string]string{
	"natskv": module + "/internal/persistence/natskv",
}

// targetImports maps target provider names to their import paths.
// http is always-on, so it has no entry here.
var targetImports = map[string]string{
	"unix": module + "/internal/target/unix",
	"grpc": module + "/internal/target/grpc",
}

// observabilityImports maps observer provider names to their import paths.
// prometheus, sql, stdout, webhook, noop are always-on, so they have no entries here.
var observabilityImports = map[string]string{
	"otel": module + "/internal/observability/otel",
}

func main() {
	configPath := flag.String("config", "lens.yaml", "Path to lens.yaml")
	outputPath := flag.String("output", "./lens", "Output binary path")
	keep := flag.Bool("keep", false, "Keep providers_gen.go after build")
	dryRun := flag.Bool("dry-run", false, "Print generated file and build command without running")
	flag.Parse()

	// Find the project root (directory containing go.mod).
	projectRoot, err := findProjectRoot()
	if err != nil {
		log.Fatalf("lens-build: %v\nRun lens-build from within the Lens project directory.", err)
	}

	// Resolve config path relative to cwd, then load.
	// A missing file is not fatal: build with always-on providers only.
	cfg, err := config.Load(*configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("lens-build: %s not found, building with always-on providers only", *configPath)
		} else {
			log.Fatalf("lens-build: load config %s: %v", *configPath, err)
		}
	}

	// Collect the set of import paths needed.
	imports := collectImports(cfg)

	// Generate providers_gen.go content.
	src := generateSource(imports)

	genFile := filepath.Join(projectRoot, "providers_gen.go")

	if *dryRun {
		fmt.Println("# providers_gen.go")
		fmt.Println(string(src))
		fmt.Printf("\n# build command\ngo build -o %s %s\n", *outputPath, projectRoot)
		return
	}

	// Write providers_gen.go.
	if err := os.WriteFile(genFile, src, 0644); err != nil {
		log.Fatalf("lens-build: write %s: %v", genFile, err)
	}

	if !*keep {
		defer func() {
			if err := os.Remove(genFile); err != nil && !os.IsNotExist(err) {
				log.Printf("lens-build: cleanup %s: %v", genFile, err)
			}
		}()
	}

	// Run go build.
	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		log.Fatalf("lens-build: resolve output path: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", absOutput, projectRoot)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	log.Printf("lens-build: compiling %d config-selected providers + always-on set", len(imports))
	if err := cmd.Run(); err != nil {
		log.Fatalf("lens-build: go build failed: %v", err)
	}
	log.Printf("lens-build: binary written to %s", absOutput)
}

// collectImports returns the import paths for providers declared in cfg,
// excluding always-on providers (those are hardcoded in main.go).
func collectImports(cfg config.File) []string {
	seen := map[string]bool{}
	var paths []string

	add := func(registry map[string]string, name string) {
		if name == "" {
			return
		}
		path, ok := registry[name]
		if !ok || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}

	add(transportImports, cfg.Transport.ProviderName())
	add(discoveryImports, cfg.Discovery.ProviderName())
	add(persistenceImports, cfg.Persistence.ProviderName())
	add(targetImports, cfg.Target.ProviderName())
	for _, p := range cfg.Observer.Providers {
		add(observabilityImports, p.ProviderName())
	}

	return paths
}

// generateSource produces a gofmt-formatted providers_gen.go file.
func generateSource(imports []string) []byte {
	var b strings.Builder
	b.WriteString("// Code generated by lens-build. DO NOT EDIT.\n")
	b.WriteString("package main\n")

	if len(imports) > 0 {
		b.WriteString("\nimport (\n")
		for _, imp := range imports {
			fmt.Fprintf(&b, "\t_ %q\n", imp)
		}
		b.WriteString(")\n")
	}

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		// format.Source should never fail on well-formed input.
		log.Fatalf("lens-build: format generated source: %v", err)
	}
	return src
}

// findProjectRoot walks up from the current directory looking for go.mod.
func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	start := dir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found (searched from %s upward)", start)
}
