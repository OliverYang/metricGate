// Command osv2policy is the adapter entry of the supply-chain compatibility
// layer: it turns external CLI output (osv-scanner JSON reports, OSV.dev
// advisory JSON) into candidate policy rows and, on request, merges them
// into a patched policy YAML. It deliberately lives under scripts/ — the
// metricgate binary never grows a feed subcommand; this is an ops glue tool.
//
//	go run ./scripts/osv2policy -in report.json -config policy.yaml
//	go run ./scripts/osv2policy -in report.json -config policy.yaml -out patch.yaml
//
// The orchestrating shell wrapper (scripts/osv2policy.sh) invokes the
// external CLIs and hands their output here. Human gates are preserved:
// automation stops at candidates + the patched file — review, fixture-smoke,
// then commit (SOP steps 2-5). The -config input is never written.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"metricgate/internal/config"
	"metricgate/internal/feed"

	"gopkg.in/yaml.v3"
)

func main() {
	var in, configPath, out, format string
	flag.StringVar(&in, "in", "", "JSON input: osv-scanner report or OSV advisory ('-' = stdin)")
	flag.StringVar(&configPath, "config", "policy.yaml", "policy file to dedup against")
	flag.StringVar(&out, "out", "", "write the merged policy to this path (plan-only when omitted)")
	flag.StringVar(&format, "format", "text", "text|json plan output")
	flag.Parse()
	if in == "" {
		fmt.Fprintln(os.Stderr, "osv2policy: -in is required (external CLI output: osv-scanner report or OSV JSON)")
		os.Exit(2)
	}
	data, err := readInput(in)
	if err != nil {
		fail(err)
	}
	vulns, err := feed.ParseAny(data)
	if err != nil {
		fail(err)
	}
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		fail(fmt.Errorf("dedup policy %s: %w", configPath, err))
	}
	byLang := map[string][]feed.BanRow{}
	for lang := range cfg.Languages {
		for _, b := range cfg.Bans(lang) {
			byLang[lang] = append(byLang[lang], feed.BanRow{Kind: b.Kind, Match: b.Match})
		}
	}
	res, groups := feed.PlanGroups(vulns, feed.DedupKeys(byLang))
	renderPlan(res, format)
	if out == "" {
		return
	}
	if filepath.Clean(out) == filepath.Clean(configPath) {
		fail(fmt.Errorf("refusing to write the -config input; pass a separate -out path"))
	}
	var base []byte
	if src, err := os.ReadFile(configPath); err == nil {
		base = src
	} else {
		fail(err)
	}
	merged, err := feed.Merge(base, groups)
	if err != nil {
		fail(err)
	}
	var probe map[string]any
	if err := yaml.Unmarshal([]byte(merged), &probe); err != nil {
		fail(fmt.Errorf("merged policy does not parse; refusing to write: %w", err))
	}
	if err := os.WriteFile(out, []byte(merged), 0o644); err != nil {
		fail(err)
	}
	fmt.Fprintf(os.Stderr, "osv2policy: wrote %s — review the + rows, fixture-smoke (SOP steps 2-5), then commit\n", out)
}

func readInput(in string) ([]byte, error) {
	if in == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(in)
}

// renderPlan prints the feed plan: + new bans, = deduped against the live
// policy, - skipped (unsupported ecosystem → ledger, never the gate).
func renderPlan(res feed.Result, format string) {
	if format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			fail(err)
		}
	} else {
		for _, v := range res.Added {
			fmt.Printf("+ [%s] %s  %s (%s)\n", "import", v.Match, v.ID, v.CVE)
			if v.Summary != "" {
				fmt.Printf("    %s\n", v.Summary)
			}
		}
		for _, v := range res.Existing {
			fmt.Printf("= [%s] %s  %s — 与现有 policy 重复\n", "import", v.Match, v.ID)
		}
		for _, v := range res.Skipped {
			fmt.Printf("- %s %s  台账：暂无源码 import 词汇（依赖树工具管辖）\n", v.Ecosystem, v.Package)
		}
	}
	fmt.Fprintf(os.Stderr, "osv2policy: %d new / %d existing / %d skipped\n", len(res.Added), len(res.Existing), len(res.Skipped))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "osv2policy:", err)
	os.Exit(1)
}
