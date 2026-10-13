package cockpit

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"metricgate/internal/config"
	"metricgate/internal/configpath"
)

// policyRequest is what the UI sends to write a policy file.
//
// Both fields are required and neither is optional-by-convention: Path keeps
// the write aimed at a file the discovery chain actually found (never at a
// new file the caller invented), and Content is the whole document so a save
// is a diff a human reviews in their editor, not a merge the tool performed.
type policyRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// policyMaxBytes caps a saved document. A policy is configuration; anything
// past a few score kilobytes is either generated or pasted by mistake, and
// the cap keeps a runaway request from writing a file nobody can read back.
const policyMaxBytes = 256 * 1024

// policyView is the parsed, inspectable form of a policy file. The UI shows
// the raw text for editing and this structure for navigation, because the
// two answer different questions: "what would I type" and "what does it mean".
type policyView struct {
	// Path is the discovered file, or "" when none was found.
	Path string `json:"path"`
	// Name is the candidate that matched (".metricgate.yaml", "policy.yaml").
	Name string `json:"name"`
	// Root is the directory the search started from.
	Root string `json:"root"`
	// Tried lists the candidates that did not exist, so a UI can say why it
	// is showing an empty editor.
	Tried []string `json:"tried,omitempty"`
	// StoppedAt is the highest directory searched (usually the repo root).
	StoppedAt string `json:"stoppedAt"`
	// Found reports whether a file exists to edit at all.
	Found bool `json:"found"`
	// Content is the file verbatim, comments included: it is the editing
	// surface and a round trip through the parser would lose annotations.
	Content string `json:"content,omitempty"`
	// ModTime is RFC3339, for a "changed on disk since you loaded this" hint.
	ModTime string `json:"modTime,omitempty"`
	// Extends lists the parent documents this policy pulls in.
	Extends []extendsView `json:"extends,omitempty"`
	// Sections are the top-level keys of the document, sorted, each with the
	// line it starts on so a click can jump to it.
	Sections []policySection `json:"sections,omitempty"`
	// Keys is the effective configuration with per-key provenance: builtin,
	// this policy, a preset, or an extended parent.
	Keys []policyKey `json:"keys,omitempty"`
	// Candidates is the discovery chain in priority order, so the UI can show
	// where this file sits and which sibling names it beat.
	Candidates []string `json:"candidates"`
	// Error is set when the file exists but could not be parsed; the UI must
	// then show the document as text and say it is invalid rather than
	// pretending an empty config is the truth.
	Error string `json:"error,omitempty"`
}

// extendsView mirrors one parent document of an inheritance chain.
type extendsView struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Exists bool   `json:"exists"`
}

// policySection is a top-level YAML key with its starting line.
type policySection struct {
	Key  string `json:"key"`
	Line int    `json:"line"`
}

// policyKey is one effective value and where it came from.
type policyKey struct {
	// Key is dotted, e.g. "languages.go.metrics.cyclomatic-complexity".
	Key string `json:"key"`
	// Source is "builtin", "policy_file", "preset:<name>" or a parent path.
	Source string `json:"source"`
	// Explicit reports whether this document wrote the key itself.
	Explicit bool `json:"explicit"`
	// Value is the effective value rendered compactly for display.
	Value string `json:"value"`
}

// policyAPI serves GET and PUT on the policy file the discovery chain picks
// for the working directory.
//
// The guard rails are deliberate. GET never writes. PUT writes only a file
// the chain already found (so no command line ever names a path the tool then
// reinterprets), validates the document parses before touching the disk, and
// replaces it atomically so a crash mid-write cannot leave a half policy.
func policyAPI() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			servePolicyView(w, r)
		case http.MethodPut:
			writePolicy(w, r)
		default:
			w.Header().Set("Allow", "GET, PUT")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// policyRoot answers "which repository is this dashboard looking at". The
// dashboard is served from the repository being audited, so the discovery
// starts in the process working directory — the same origin `cockpit serve`
// was started from.
func policyRoot() string {
	root, err := os.Getwd()
	if err != nil {
		return "."
	}
	return root
}

// discoveredPolicy runs the discovery chain and records what it saw.
func discoveredPolicy() configpath.Found {
	return configpath.Discover("", policyRoot())
}

// servePolicyView answers GET: the discovered file, its raw text, its parsed
// shape, and the effective values with provenance.
func servePolicyView(w http.ResponseWriter, r *http.Request) {
	view := policyViewOf(discoveredPolicy())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

// policyViewOf assembles the inspectable form of the discovered policy.
func policyViewOf(found configpath.Found) policyView {
	view := policyView{
		Root:       found.Root,
		Name:       found.Name,
		Tried:      found.Tried,
		StoppedAt:  found.StoppedAt,
		Path:       found.Path,
		Found:      found.FoundAny(),
		Candidates: append([]string(nil), configpath.CandidateNames...),
	}
	if view.Found {
		view.Content, view.Error, view.ModTime = readPolicyFile(found.Path)
		view.Sections = policySections(view.Content)
		view.Extends = policyExtends(found.Path)
	}
	if cfg, err := loadPolicyForView(found); err == nil {
		view.Keys = policyKeys(cfg, found.Path)
	}
	return view
}

// readPolicyFile returns the document, a parse error if it is not valid YAML
// that this tool understands, and the modification time.
//
// A parse error is returned rather than swallowed: the caller shows the text
// and the error, because silently presenting an invalid policy as "nothing
// configured" is the same lie a missing capability tells.
func readPolicyFile(path string) (content, parseErr, modTime string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err.Error(), ""
	}
	content = string(data)
	if info, statErr := os.Stat(path); statErr == nil {
		modTime = info.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if _, err := config.Load(data); err != nil {
		return content, err.Error(), modTime
	}
	return content, "", modTime
}

// policySections lists the top-level keys with their 1-based line numbers, so
// the UI can jump to a section in the editor.
func policySections(content string) []policySection {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil
	}
	root := documentRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]policySection, 0, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		out = append(out, policySection{
			Key:  root.Content[i].Value,
			Line: root.Content[i].Line,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// documentRoot unwraps the document node yaml.v3 puts at the top of every
// parse, returning nil for an empty document.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind == 0 {
		return nil
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

// policyExtends resolves the inheritance chain of a policy, so the UI can show
// that a value lives in a parent document.
func policyExtends(path string) []extendsView {
	cfg, err := config.LoadFile(path)
	if err != nil {
		return nil
	}
	out := make([]extendsView, 0, 2)
	for _, src := range cfg.Extends() {
		out = append(out, extendsView{
			Path:   src.Path,
			Kind:   "extends",
			Exists: isRegularFile(src.Path),
		})
	}
	return out
}

// policyKeys lists the effective keys of the loaded policy with provenance.
// The list is capped and sorted so the payload stays small and stable: a
// dashboard render must not depend on map iteration order.
func policyKeys(cfg *config.Config, policyPath string) []policyKey {
	if cfg == nil {
		return nil
	}
	keys := sortedExplicitKeys(cfg)
	out := make([]policyKey, 0, len(keys))
	for _, key := range keys {
		source, path, explicit := cfg.ValueSource(key, "builtin")
		out = append(out, policyKey{
			Key:      key,
			Source:   displaySource(source, path, policyPath),
			Explicit: explicit,
			Value:    "",
		})
	}
	if len(out) > policyMaxKeys {
		out = out[:policyMaxKeys]
	}
	return out
}

// sourcePolicyFile / sourceBuiltin are config's provenance labels, restated
// here because cockpit shows them to a human: a value must be traceable to
// "this policy", "builtin", "preset:x" or an "extends" parent, and a missing
// label would silently blame the repository for a default.
const (
	sourcePolicyFile = "policy_file"
	sourceBuiltin    = "builtin"
)

// policyMaxKeys bounds the provenance list. It exists for the same reason as
// the character budget elsewhere in the dashboard: a bounded payload is a
// predictable one.
const policyMaxKeys = 200

// sortedExplicitKeys returns the policy's known dotted keys in a stable order.
func sortedExplicitKeys(cfg *config.Config) []string {
	keys := make([]string, 0, len(cfg.ExplicitKeys))
	for key, explicit := range cfg.ExplicitKeys {
		if explicit {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// displaySource turns config provenance into one short label. A policy file
// that is the discovered one reads as "this policy"; anything else keeps its
// path so the origin stays traceable.
func displaySource(source, path, discovered string) string {
	if source == sourcePolicyFile {
		if discovered != "" && sameFilePath(path, discovered) {
			return "this policy"
		}
		if path == "" {
			return "policy"
		}
		return path
	}
	if path != "" {
		return source + " " + path
	}
	return source
}

// sameFilePath compares two paths after resolving symlinks, because a macOS
// temp path and its /private counterpart are the same file.
func sameFilePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// isRegularFile reports whether path names a readable file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// loadPolicyForView loads the policy the discovery chain picked so the UI can
// show effective values and their provenance. A missing or unreadable policy
// degrades to the built-in defaults rather than failing the view: the
// dashboard's job is to answer "what is in force", and the built-in defaults
// are a true answer when nothing else is configured.
func loadPolicyForView(found configpath.Found) (*config.Config, error) {
	if !found.FoundAny() {
		return loadPolicyQuiet("")
	}
	return config.LoadFile(found.Path)
}

// loadPolicyQuiet loads a policy without any note on stderr: the cockpit is a
// read-mostly view and must not narrate its own degradation. A missing file
// falls back to the built-in defaults, matching the CLI's own behaviour.
func loadPolicyQuiet(path string) (*config.Config, error) {
	if path == "" {
		cfg, _ := config.Default()
		return cfg, nil
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
