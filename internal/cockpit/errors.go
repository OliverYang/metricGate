package cockpit

import (
	"encoding/json"
	"net/http"
)

// writeJSONError is the single failure shape of every cockpit endpoint: a status
// code plus a JSON body carrying the same message text.
//
// http.Error served a plain-text body, which the browser client could not read:
// its fetch wrapper treats a non-JSON error body as "no message" and falls back
// to "请求失败 (500)", so the operator saw a status code where the server had
// written a sentence. Changing the shape rather than the parser keeps the text
// the only thing that travels — no message here is a client-side invention.
//
// The message is written verbatim by every caller on purpose:
// "unknown scope (known projects: …)" is load-bearing for the scope contract,
// and a rewritten message would answer a different question than the one asked.
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
