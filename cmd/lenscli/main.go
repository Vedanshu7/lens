// lenscli inspects a running Lens sidecar from the terminal.
//
// Usage:
//
//	lenscli <command> [flags]
//
// Commands:
//
//	status      Show provider stack and health
//	peers       List live peers for a service
//	invalidate  Trigger a manual cache invalidation
//	replay      Show the invalidation replay log
//	events      Stream live invalidation events (requires SSE endpoint)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "status":
		runStatus(args)
	case "peers":
		runPeers(args)
	case "invalidate":
		runInvalidate(args)
	case "replay":
		runReplay(args)
	case "events":
		runEvents(args)
	case "version", "--version", "-version":
		fmt.Println("lenscli", version)
	case "help", "--help", "-help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `lenscli — Lens sidecar inspector

USAGE
  lenscli <command> [flags]

COMMANDS
  status      Show provider stack and health of a sidecar
  peers       List live peers for a service
  invalidate  Trigger a manual cache invalidation
  replay      Show the invalidation replay log
  events      Stream live invalidation events via SSE

FLAGS (common to all commands)
  --addr  string   Sidecar HTTP address (default "http://localhost:8900")
  --token string   x-lens-token for authenticated sidecars
  --json           Output raw JSON instead of a table

Run 'lenscli <command> --help' for command-specific flags.`)
}

// --- helpers ---

type commonFlags struct {
	addr  string
	token string
	json  bool
	fs    *flag.FlagSet
}

func newFlags(name string) *commonFlags {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	cf := &commonFlags{fs: fs}
	fs.StringVar(&cf.addr, "addr", "http://localhost:8900", "sidecar HTTP address")
	fs.StringVar(&cf.token, "token", os.Getenv("LENS_TOKEN"), "x-lens-token (defaults to $LENS_TOKEN)")
	fs.BoolVar(&cf.json, "json", false, "output raw JSON")
	return cf
}

func (cf *commonFlags) parse(args []string) { cf.fs.Parse(args) } //nolint:errcheck

func (cf *commonFlags) get(path string) (map[string]any, error) {
	url := strings.TrimRight(cf.addr, "/") + path
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if cf.token != "" {
		req.Header.Set("x-lens-token", cf.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", cf.addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func (cf *commonFlags) post(path string, payload any) (map[string]any, error) {
	b, _ := json.Marshal(payload)
	url := strings.TrimRight(cf.addr, "/") + path
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cf.token != "" {
		req.Header.Set("x-lens-token", cf.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", cf.addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out map[string]any
	json.Unmarshal(body, &out) //nolint:errcheck
	return out, nil
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint:errcheck
}

func die(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+msg+"\n", args...)
	os.Exit(1)
}

// --- status ---

func runStatus(args []string) {
	cf := newFlags("status")
	cf.parse(args)

	data, err := cf.get("/api/health")
	if err != nil {
		die("%v", err)
	}
	if cf.json {
		printJSON(data)
		return
	}

	redis, _ := data["redis"].(bool)
	target, _ := data["target"].(bool)

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tSTATUS")
	fmt.Fprintf(tw, "redis\t%s\n", statusBadge(redis))
	fmt.Fprintf(tw, "target\t%s\n", statusBadge(target))

	if providers, ok := data["providers"].(map[string]any); ok {
		fmt.Fprintln(tw, "---\t---")
		for _, k := range []string{"transport", "persistence", "discovery", "target"} {
			if v, ok := providers[k].(string); ok && v != "" {
				fmt.Fprintf(tw, "%s\t%s\n", k, v)
			}
		}
		if obs, ok := providers["observers"].([]any); ok && len(obs) > 0 {
			names := make([]string, len(obs))
			for i, o := range obs {
				names[i], _ = o.(string)
			}
			fmt.Fprintf(tw, "observers\t%s\n", strings.Join(names, ", "))
		}
	}
	tw.Flush()
}

func statusBadge(ok bool) string {
	if ok {
		return "ok"
	}
	return "DOWN"
}

// --- peers ---

func runPeers(args []string) {
	cf := newFlags("peers")
	var svc string
	cf.fs.StringVar(&svc, "service", "", "service name (required)")
	cf.parse(args)

	if svc == "" {
		die("--service is required")
	}

	data, err := cf.get("/api/nodes?service=" + svc)
	if err != nil {
		die("%v", err)
	}
	if cf.json {
		printJSON(data)
		return
	}

	instances, _ := data["instances"].([]any)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE\tAGENT URL")
	for _, raw := range instances {
		m, _ := raw.(map[string]any)
		inst, _ := m["instance"].(string)
		url, _ := m["agentUrl"].(string)
		fmt.Fprintf(tw, "%s\t%s\n", inst, url)
	}
	tw.Flush()
	fmt.Printf("\n%d peer(s)\n", len(instances))
}

// --- invalidate ---

func runInvalidate(args []string) {
	cf := newFlags("invalidate")
	var svc, pattern string
	cf.fs.StringVar(&svc, "service", "", "service name (required)")
	cf.fs.StringVar(&pattern, "pattern", "", "glob pattern (optional, e.g. user:*)")
	cf.parse(args)

	if svc == "" {
		die("--service is required")
	}

	payload := map[string]any{"service": svc}
	if pattern != "" {
		payload["pattern"] = pattern
	}

	data, err := cf.post("/api/invalidate", payload)
	if err != nil {
		die("%v", err)
	}
	if cf.json {
		printJSON(data)
		return
	}

	if data == nil {
		fmt.Println("queued (batching enabled)")
		return
	}
	total, _ := data["total"].(float64)
	confirmed, _ := data["confirmed"].(float64)
	fmt.Printf("invalidated: %d/%d instances confirmed\n", int(confirmed), int(total))
}

// --- replay ---

func runReplay(args []string) {
	cf := newFlags("replay")
	var limit int
	cf.fs.IntVar(&limit, "limit", 20, "max number of entries to show")
	cf.parse(args)

	path := fmt.Sprintf("/api/audit?limit=%d", limit)
	data, err := cf.get(path)
	if err != nil {
		die("%v", err)
	}
	if cf.json {
		printJSON(data)
		return
	}

	entries, _ := data["entries"].([]any)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tSERVICE\tPATTERN\tCONFIRMED/TOTAL")
	for _, raw := range entries {
		m, _ := raw.(map[string]any)
		ts, _ := m["ts"].(string)
		svc, _ := m["service"].(string)
		pat, _ := m["pattern"].(string)
		if pat == "" {
			pat = "*"
		}
		confirmed, _ := m["confirmed"].(float64)
		total, _ := m["total"].(float64)
		// Reformat timestamp for readability.
		t, err := time.Parse(time.RFC3339, ts)
		if err == nil {
			ts = t.Local().Format("15:04:05")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\n", ts, svc, pat, int(confirmed), int(total))
	}
	tw.Flush()
}

// --- events ---

func runEvents(args []string) {
	cf := newFlags("events")
	cf.parse(args)

	url := strings.TrimRight(cf.addr, "/") + "/api/events/stream"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		die("%v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if cf.token != "" {
		req.Header.Set("x-lens-token", cf.token)
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		die("connect %s: %w — is the sidecar running with SSE enabled?", cf.addr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		die("/api/events/stream not available on this sidecar version")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		die("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	fmt.Fprintf(os.Stderr, "streaming events from %s (Ctrl-C to stop)...\n\n", cf.addr)

	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			os.Stdout.Write(buf[:n]) //nolint:errcheck
		}
		if err != nil {
			if err == io.EOF {
				fmt.Fprintln(os.Stderr, "\nstream closed")
			} else {
				die("read error: %v", err)
			}
			return
		}
	}
}
