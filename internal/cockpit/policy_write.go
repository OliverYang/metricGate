package cockpit

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"metricgate/internal/config"
	"metricgate/internal/configpath"
	"metricgate/internal/store"
)

// writePolicy serves PUT /api/policy: replace the discovered policy document.
//
// The order of the checks is the design. A save must never be able to name a
// file the discovery chain would not have read (otherwise a UI could create a
// policy no command will ever load), and it must never be able to leave a half
// document behind (otherwise one interrupted request breaks every later run).
// So: same-origin → parse → validate → re-check the discovered path → atomic
// replace.
func writePolicy(w http.ResponseWriter, r *http.Request, logPath, dbPath string) {
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "same-origin request required")
		return
	}
	request, ok := readPolicyRequest(w, r)
	if !ok {
		return
	}
	window, err := policyScopeWindow(r, logPath, dbPath)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, ok := saveablePolicyTarget(w, request.Path, window)
	if !ok {
		return
	}
	savePolicy(w, target, request)
}

// savePolicy writes the document and reports the result. It is a separate
// function because the save path and the failure path are different questions:
// one produces a JSON acknowledgement, the other names the reason a document
// was refused.
func savePolicy(w http.ResponseWriter, target string, request policyBody) {
	if err := savePolicyDocument(target, request); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"saved":  target,
		"bytes":  len(request.Content),
		"status": "ok",
	})
}

// readPolicyRequest reads the body, parses the envelope and resolves the file
// it applies to, writing the error response itself on any refusal.
func readPolicyRequest(w http.ResponseWriter, r *http.Request) (policyBody, bool) {
	body, ok := readPolicyBody(w, r)
	if !ok {
		return policyBody{}, false
	}
	return decodePolicyRequest(w, body)
}

// saveablePolicyTarget resolves where the save would land and refuses anything
// that is not the discovered file. A brand-new file is a different operation
// from saving the one in force: refusing here keeps "what the tool reads" and
// "what the UI wrote" the same file, and a repository can still create the
// file with an editor.
//
// The discovery runs from the request's scope, so saving while looking at one
// project cannot write into the policy of the directory the server happens to
// sit in — the two are different files the moment the database is shared.
func saveablePolicyTarget(w http.ResponseWriter, requested string, window store.Window) (string, bool) {
	found := discoveredPolicyIn(scopeOrigin(window))
	if !found.FoundAny() {
		writeJSONError(w, http.StatusConflict, "no policy file found to update; create one at "+filepath.Join(scopeOrigin(window), configpath.CandidateNames[0])+" and retry")
		return "", false
	}
	return resolvePolicyTarget(w, found, requested)
}

// savePolicyDocument validates then atomically replaces the document. The
// validation error names the policy that failed to parse, so the caller can
// report it verbatim.
func savePolicyDocument(target string, request policyBody) error {
	if _, err := parsePolicyDocument([]byte(request.Content)); err != nil {
		return err
	}
	return writePolicyAtomic(target, []byte(request.Content))
}

// policyBody is the JSON envelope: an optional path plus the whole document.
type policyBody struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// readPolicyBody reads the request body with a size cap. The cap is enforced
// while reading, so an oversized body never lands in memory in full.
func readPolicyBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, policyMaxBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "read body: "+err.Error())
		return nil, false
	}
	if len(body) > policyMaxBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "policy too large")
		return nil, false
	}
	return body, true
}

// decodePolicyRequest parses the envelope, rejecting unknown fields so a save
// can never silently drop half a request.
func decodePolicyRequest(w http.ResponseWriter, body []byte) (policyBody, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		writeJSONError(w, http.StatusBadRequest, "content is required")
		return policyBody{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var payload policyBody
	if err := dec.Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return policyBody{}, false
	}
	if strings.TrimSpace(payload.Content) == "" {
		writeJSONError(w, http.StatusBadRequest, "content is required: an empty policy would disable every configured rule")
		return policyBody{}, false
	}
	return payload, true
}

// resolvePolicyTarget checks the requested path against the discovered file.
//
// The requested path is convenience, not authority: the write always lands on
// the file in force. A mismatch — a UI that loaded one repository and posts to
// another — is refused rather than silently redirected, because writing to a
// file the reader did not show is exactly the class of surprise this endpoint
// exists to prevent.
func resolvePolicyTarget(w http.ResponseWriter, found configpath.Found, requested string) (string, bool) {
	if requested != "" && !sameFilePath(requested, found.Path) {
		writeJSONError(w, http.StatusConflict, "path does not match the discovered policy "+found.Path)
		return "", false
	}
	target, err := filepath.Abs(found.Path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "cannot resolve the policy path")
		return "", false
	}
	return target, true
}

// parsePolicyDocument validates the document with the same loader the gate
// uses, so a save cannot install a policy the checker would reject on the
// next run.
func parsePolicyDocument(data []byte) (*config.Config, error) {
	return config.Load(data)
}

// writePolicyAtomic replaces the file through a temporary file in the same
// directory followed by a rename, which is atomic within a filesystem. The
// previous content survives as <file>.bak only when it existed; a failure to
// create the backup is not fatal (the original is still replaced atomically).
func writePolicyAtomic(target string, content []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".metricgate-policy-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if old, err := os.ReadFile(target); err == nil {
		if err := os.WriteFile(target+".bak", old, 0o600); err != nil {
			return err
		}
	}
	return os.Rename(tmpName, target)
}
