package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/dejo1307/archmcp/internal/config"
	"github.com/dejo1307/archmcp/internal/engine"
	"github.com/dejo1307/archmcp/internal/explainers/apisurface"
	"github.com/dejo1307/archmcp/internal/explainers/cohesion"
	"github.com/dejo1307/archmcp/internal/explainers/coupling"
	"github.com/dejo1307/archmcp/internal/explainers/coverage"
	"github.com/dejo1307/archmcp/internal/explainers/cycles"
	"github.com/dejo1307/archmcp/internal/explainers/deadcode"
	"github.com/dejo1307/archmcp/internal/explainers/depdepth"
	"github.com/dejo1307/archmcp/internal/explainers/godmodule"
	"github.com/dejo1307/archmcp/internal/explainers/layers"
	"github.com/dejo1307/archmcp/internal/explainers/stability"
	"github.com/dejo1307/archmcp/internal/explainers/testmap"
	"github.com/dejo1307/archmcp/internal/extractors/goextractor"
	"github.com/dejo1307/archmcp/internal/extractors/kotlinextractor"
	"github.com/dejo1307/archmcp/internal/extractors/openapiextractor"
	"github.com/dejo1307/archmcp/internal/extractors/pythonextractor"
	"github.com/dejo1307/archmcp/internal/extractors/rubyextractor"
	"github.com/dejo1307/archmcp/internal/extractors/rustextractor"
	"github.com/dejo1307/archmcp/internal/extractors/swiftextractor"
	"github.com/dejo1307/archmcp/internal/extractors/tsextractor"
	"github.com/dejo1307/archmcp/internal/facts"
	"github.com/dejo1307/archmcp/internal/renderers/llmcontext"
	"github.com/dejo1307/archmcp/internal/server"
)

// Version is set via ldflags at build time:
//
//	go build -ldflags "-X main.Version=v1.0.0" ./cmd/archmcp
var Version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the real entry point, extracted for testability.
// It returns an exit code (0 = success, non-zero = error).
func run(args []string, stdout, stderr io.Writer) int {
	// Handle --version/-v and --help/-h BEFORE any initialization.
	for _, arg := range args {
		switch arg {
		case "--version", "-v":
			fmt.Fprintf(stdout, "archmcp %s\n", Version)
			return 0
		case "--help", "-h":
			printUsage(stdout)
			return 0
		}
	}

	// Ensure log output goes to stderr, never stdout (MCP uses stdout for JSON-RPC)
	log.SetOutput(stderr)

	ctx := context.Background()

	// Parse remaining flags
	generateMode := false
	cfgPath := "mcp-arch.yaml"
	for _, arg := range args {
		switch arg {
		case "--generate":
			generateMode = true
		default:
			cfgPath = arg
		}
	}

	// If the config path is relative, resolve it first against the current
	// working directory, then (as a fallback) against the directory containing
	// the binary itself. This ensures the config is found when Cursor starts
	// the MCP server from a different working directory.
	cfg, err := config.Load(cfgPath)
	if err != nil && !filepath.IsAbs(cfgPath) {
		if exePath, exErr := os.Executable(); exErr == nil {
			exeDir := filepath.Dir(exePath)
			cfg, err = config.Load(filepath.Join(exeDir, cfgPath))
		}
	}
	if err != nil {
		// If config file doesn't exist, use defaults
		fmt.Fprintf(stderr, "warning: %v, using defaults\n", err)
		cfg = config.Default()
	}

	eng, err := engine.New(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "failed to create engine: %v\n", err)
		return 1
	}

	// Register extractors
	eng.RegisterExtractor(goextractor.New())
	eng.RegisterExtractor(kotlinextractor.New())
	eng.RegisterExtractor(openapiextractor.New())
	eng.RegisterExtractor(pythonextractor.New())
	eng.RegisterExtractor(tsextractor.New())
	eng.RegisterExtractor(swiftextractor.New())
	eng.RegisterExtractor(rubyextractor.New())
	eng.RegisterExtractor(rustextractor.New())

	// Register explainers
	eng.RegisterExplainer(apisurface.New())
	eng.RegisterExplainer(cohesion.New())
	eng.RegisterExplainer(coupling.New())
	eng.RegisterExplainer(coverage.New())
	eng.RegisterExplainer(cycles.New())
	eng.RegisterExplainer(deadcode.New())
	eng.RegisterExplainer(depdepth.New())
	eng.RegisterExplainer(godmodule.New())
	eng.RegisterExplainer(layers.New())
	eng.RegisterExplainer(stability.New())
	eng.RegisterExplainer(testmap.New())

	// Register renderers
	eng.RegisterRenderer(llmcontext.New(cfg.Output.MaxContextTokens))

	// One-shot generation mode
	if generateMode {
		repoPath, err := filepath.Abs(cfg.Repo)
		if err != nil {
			fmt.Fprintf(stderr, "failed to resolve repo path: %v\n", err)
			return 1
		}

		snapshot, err := eng.GenerateSnapshot(ctx, repoPath, false)
		if err != nil {
			fmt.Fprintf(stderr, "snapshot generation failed: %v\n", err)
			return 1
		}

		if err := eng.WriteArtifacts(repoPath); err != nil {
			fmt.Fprintf(stderr, "failed to write artifacts: %v\n", err)
			return 1
		}

		fmt.Fprintf(stderr, "\nSnapshot complete:\n")
		fmt.Fprintf(stderr, "  Repository:  %s\n", snapshot.Meta.RepoPath)
		fmt.Fprintf(stderr, "  Facts:       %d\n", snapshot.Meta.FactCount)
		fmt.Fprintf(stderr, "  Insights:    %d\n", snapshot.Meta.InsightCount)
		fmt.Fprintf(stderr, "  Artifacts:   %d\n", len(snapshot.Artifacts))
		fmt.Fprintf(stderr, "  Duration:    %s\n", snapshot.Meta.Duration)
		fmt.Fprintf(stderr, "  Output:      %s\n", filepath.Join(repoPath, cfg.Output.Dir))
		return 0
	}

	// Auto-load existing snapshot if available (so queries work immediately
	// without requiring a generate_snapshot call first).
	if repoPath, err := filepath.Abs(cfg.Repo); err == nil {
		factsPath := filepath.Join(repoPath, cfg.Output.Dir, "facts.jsonl")
		if _, err := os.Stat(factsPath); err == nil {
			log.Printf("[main] loading existing snapshot from %s", factsPath)
			if err := eng.Store().ReadJSONLFile(factsPath); err != nil {
				log.Printf("[main] warning: failed to load existing facts: %v", err)
			} else {
				repoLabel := filepath.Base(repoPath)
				eng.Store().SetRepoRange(0, repoLabel)
				eng.Store().BuildGraph()
				eng.SetSnapshot(&facts.Snapshot{
					Meta: facts.SnapshotMeta{RepoPath: repoPath},
				})
				log.Printf("[main] loaded %d facts from existing snapshot", eng.Store().Count())
			}
		}
	}

	// MCP server mode (default)
	srv, err := server.New(eng, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "failed to create server: %v\n", err)
		return 1
	}

	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "server error: %v\n", err)
		return 1
	}

	return 0
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `archmcp %s - Architectural MCP server for code analysis

Usage:
  archmcp [flags] [config-file]

Flags:
  --version, -v    Print version and exit
  --help, -h       Print this help message and exit
  --generate       Generate architectural snapshot and exit

Arguments:
  config-file      Path to config file (default: mcp-arch.yaml)

Examples:
  archmcp                          Start MCP server with default config
  archmcp mcp-arch.yaml            Start MCP server with specified config
  archmcp --generate               Generate snapshot and exit
  archmcp --version                Print version

Build with version:
  go build -ldflags "-X main.Version=v1.0.0" ./cmd/archmcp
`, Version)
}
