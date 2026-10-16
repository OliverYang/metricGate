package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"metricgate/internal/calllog"
	"metricgate/internal/cockpit"
)

// cmdCockpit exposes the opt-in local dashboard and manual update check.
func cmdCockpit(args []string) {
	dispatch("cockpit", "serve|update", args, map[string]func([]string){
		"serve":  cmdCockpitServe,
		"update": cmdCockpitUpdate,
	})
}

// cmdCockpitServe starts the dashboard bound to loopback by default.
func cmdCockpitServe(args []string) {
	set := flag.NewFlagSet("cockpit serve", flag.ExitOnError)
	addr := set.String("addr", "127.0.0.1:8787", "loopback address for the local dashboard")
	logPath := set.String("log", calllog.DefaultPath, "local MCP call log path (empty disables call log)")
	dbPath := set.String("db", ".metricgate/history.db", "local history database path (empty disables run history)")
	parseArgs(set, args)
	if !isLoopbackAddress(*addr) {
		fail(fmt.Errorf("cockpit serve only accepts loopback addresses; got %q", *addr))
	}
	// The build stamp is printed here on purpose. "I restarted it and the UI
	// did not change" is nearly always a stale binary, and the served assets
	// cannot tell the operator which build they came from - the page looks the
	// same either way. Printing the revision at startup makes that one glance.
	fmt.Fprintf(os.Stderr, "metricgate cockpit: serving locally at http://%s (build %s, Ctrl-C to stop)\n",
		*addr, buildStamp())
	if err := http.ListenAndServe(*addr, cockpit.Handler(*logPath, *dbPath)); err != nil {
		fail(err)
	}
}

func isLoopbackAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func cmdCockpitUpdate(args []string) {
	set := flag.NewFlagSet("cockpit update", flag.ExitOnError)
	format := set.String("format", "text", "text|json")
	feedURL := set.String("feed", os.Getenv("METRICGATE_FEED_URL"), "release feed URL; empty (default) means no feed is published yet")
	timeout := set.Duration("timeout", cockpit.DefaultTimeout, "probe timeout")
	parseArgs(set, args)

	checker := cockpit.New(toolVersion)
	checker.FeedURL = *feedURL
	checker.Timeout = *timeout

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Non-blocking: the checking state is reported before the probe runs,
	// and the probe itself lives on its own goroutine inside Check.
	ch := checker.Check(ctx)
	var result cockpit.Status
	select {
	case result = <-ch:
	case <-time.After(*timeout + 5*time.Second):
		// The channel always closes in normal operation; this belt-and-
		// braces bound converts a pathological hang into an error state.
		result = cockpit.Status{State: cockpit.StateError, Error: "update check did not complete"}
	}

	if *format == "json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fail(err)
		}
		os.Stdout.Write(append(data, '\n'))
		return
	}
	printUpdateStatus(result)
}

// printUpdateStatus renders one terminal status. Only the error case prints
// to stderr; the command still exits 0 — a failed update check must not be
// mistaken for a failed build gate.
func printUpdateStatus(s cockpit.Status) {
	switch s.State {
	case cockpit.StateAvailable:
		fmt.Printf("update available: %s -> %s\n", s.Current, s.Latest)
		if s.URL != "" {
			fmt.Println(s.URL)
		}
	case cockpit.StateNotAvail:
		fmt.Printf("up to date (%s)\n", s.Current)
	case cockpit.StateChecking:
		fmt.Println("checking for updates…")
	case cockpit.StateNotEnabled:
		// Not a failure: no release feed exists to check yet. stdout, exit 0 —
		// a script that treats a missing feed as an error would be reacting to
		// a publishing decision, not to its own work.
		fmt.Printf("update check is not enabled: %s\n", s.Error)
	case cockpit.StateError:
		fmt.Fprintf(os.Stderr, "update check failed: %s\n", s.Error)
	default:
		fmt.Fprintf(os.Stderr, "update check failed: unknown state %q\n", s.State)
	}
}
