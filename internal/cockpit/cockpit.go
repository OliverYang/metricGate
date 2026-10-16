// Package cockpit implements the dev-cockpit update check: a manual,
// user-triggered probe of the release feed that must never block the UI or
// crash the host when the network is unreachable. The check runs on its own
// goroutine and reports exactly one of four terminal states — checking,
// available, not-available (up to date) or error — through a state machine
// the Cockpit tab can render directly.
package cockpit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// States the update check moves through. Exactly one is reported per check,
// and each terminal state is reached exactly once — a UI can switch on the
// state without needing to interpret errors or payloads.
//
// StateNotEnabled exists because there is no release feed yet. A build with no
// feed configured used to probe a placeholder domain and report the DNS failure
// as StateError, which made "the product points at a host that does not exist"
// look like a transient network problem to both users and logs. Reporting
// "not enabled" states the truth instead: nothing was shipped to check against.
const (
	StateChecking   = "checking"
	StateAvailable  = "available"     // a newer release exists
	StateNotAvail   = "not_available" // running the latest release
	StateError      = "error"         // network or feed failure; never fatal
	StateNotEnabled = "not_enabled"   // no release feed configured; nothing probed
)

// DefaultTimeout bounds a single check so a hung connection cannot leave the
// tab stuck in the checking state.
const DefaultTimeout = 5 * time.Second

// Feed is the minimal release-feed document the checker understands.
type Feed struct {
	Version string `json:"version"`
	URL     string `json:"url,omitempty"`
}

// Status is one check's observable outcome. State is always one of the State*
// constants; Latest and URL are populated only for StateAvailable, and Error
// for StateError and StateNotEnabled. Time stamps when the terminal state was
// reached.
type Status struct {
	State     string    `json:"state"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	URL       string    `json:"url,omitempty"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// Checker runs manual, non-blocking update checks. Safe for concurrent use;
// a Check call supersedes any earlier in-flight check's reported status.
type Checker struct {
	// Current is the running tool's version, compared against the feed.
	Current string

	// FeedURL is the release feed endpoint. Empty means no feed is configured:
	// the check reports StateNotEnabled and makes no request.
	FeedURL string

	// Timeout bounds one HTTP probe; zero means DefaultTimeout.
	Timeout time.Duration

	// HTTPClient lets tests inject transport behavior; nil means a client
	// with the configured timeout and no cookie/header state.
	HTTPClient *http.Client

	mu     sync.Mutex
	status Status
	gen    int // generation counter; late responses from superseded checks are discarded
}

// New builds a checker for the given current version.
func New(current string) *Checker {
	return &Checker{Current: current}
}

// Status returns the last reported status. Before any check it reports
// not-checked state via an empty State; the tab renders that as idle.
func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Check starts a non-blocking update probe and immediately reports the
// checking state. The returned channel carries exactly one terminal Status
// (available, not_available, or error) and is always closed, so callers may
// range over it or select on it without leaking. Network failures are
// converted into a StateError status — Check never panics, blocks the
// caller, or requires the network to succeed.
func (c *Checker) Check(ctx context.Context) <-chan Status {
	out := make(chan Status, 1)
	c.mu.Lock()
	c.gen++
	gen := c.gen
	c.status = Status{State: StateChecking, Current: c.Current, CheckedAt: time.Now().UTC()}
	checking := c.status
	c.mu.Unlock()

	go c.run(ctx, gen, checking, out)
	return out
}

// run performs the HTTP probe off the caller's goroutine and publishes the
// terminal state. Superseded generations (a newer Check started first) drop
// their result instead of overwriting fresher status.
func (c *Checker) run(ctx context.Context, gen int, checking Status, out chan<- Status) {
	defer close(out)
	res := c.probe(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	if gen != c.gen {
		return // superseded by a newer check
	}
	res.Current = checking.Current
	res.CheckedAt = time.Now().UTC()
	c.status = res

	select {
	case out <- res:
	default:
	}
}

// probe executes the synchronous feed fetch. A missing feed is a configuration
// state, not a failure, so it returns StateNotEnabled without touching the
// network; every other failure path returns StateError. Nothing here can panic
// into the caller.
func (c *Checker) probe(ctx context.Context) Status {
	url := c.FeedURL
	if url == "" {
		return Status{State: StateNotEnabled, Error: "no release feed configured; pass -feed URL"}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return Status{State: StateError, Error: fmt.Sprintf("build request: %v", err)}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// Network unreachable, DNS failure, timeout: report, don't crash.
		return Status{State: StateError, Error: sanitizeErr(err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Status{State: StateError, Error: fmt.Sprintf("release feed returned HTTP %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Status{State: StateError, Error: sanitizeErr(err)}
	}
	var feed Feed
	if err := json.Unmarshal(body, &feed); err != nil || feed.Version == "" {
		return Status{State: StateError, Error: "release feed returned an unreadable document"}
	}

	if feed.Version == c.Current {
		return Status{State: StateNotAvail, Latest: feed.Version}
	}
	return Status{State: StateAvailable, Latest: feed.Version, URL: feed.URL}
}

// sanitizeErr strips transport internals (URLs, addresses) from network
// errors: the status string is user-facing.
func sanitizeErr(err error) string {
	if uerr, ok := err.(interface{ Unwrap() error }); ok {
		if inner := uerr.Unwrap(); inner != nil {
			return inner.Error()
		}
	}
	return err.Error()
}
