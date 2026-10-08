// package feed also adapts external CLI output: the canonical source is
// `osv-scanner scan --format json` (see docs/constraints/supply-chain-feed.md
// for the orchestration contract). The adapter lives next to the OSV parser
// and shares its derive/plan/merge pipeline so both input shapes — raw
// advisories and scanner reports — land in the same candidate rows.
package feed

import (
	"encoding/json"
	"fmt"
	"sort"
)

// ScanReport is the minimal `osv-scanner scan --format json` slice:
// results[].packages[].package{name,version,ecosystem} + vulnerabilities[]
// (full OSV records). Sources the orchestrator does not care about
// (lockfile paths, groups, call analysis) are ignored.
type ScanReport struct {
	Results []ScanResult `json:"results"`
}

type ScanResult struct {
	Source struct {
		Path string `json:"path"`
		Type string `json:"type"`
	} `json:"source"`
	Packages []ScanPackage `json:"packages"`
}

type ScanPackage struct {
	Package struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		Ecosystem string `json:"ecosystem"`
	} `json:"package"`
	Vulnerabilities []osvVuln `json:"vulnerabilities"`
}

// ParseAny sniffs the document shape: an osv-scanner report ({"results":…})
// goes to the scanner adapter, everything else ({"vulns":[…]} or a single
// vuln object) to the OSV parser.
func ParseAny(data []byte) ([]Vuln, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err == nil {
		if _, ok := probe["results"]; ok {
			return ParseScanReport(data)
		}
	}
	return ParseOSV(data)
}

// ParseScanReport flattens an osv-scanner JSON report into Vulns using the
// same derive rules as ParseOSV: one entry per (vulnerability, affected
// package) pair in a supported ecosystem, unsupported ecosystems returned
// with Lang == "" for the ledger. The same vulnerability reported from
// several sources (lockfile, SBOM, git) collapses to one entry.
func ParseScanReport(data []byte) ([]Vuln, error) {
	var doc ScanReport
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("not an osv-scanner JSON report: %w", err)
	}
	seen := map[string]bool{}
	var out []Vuln
	for _, r := range doc.Results {
		for _, sp := range r.Packages {
			for _, v := range sp.Vulnerabilities {
				aff := osvAffected{Package: osvPackage{Ecosystem: sp.Package.Ecosystem, Name: sp.Package.Name}}
				for _, cand := range v.Affected {
					if cand.Package.Ecosystem == aff.Package.Ecosystem && cand.Package.Name == aff.Package.Name {
						aff = cand
						break
					}
				}
				vu := derive(toVuln(v, aff))
				if k := vulnKey(vu); !seen[k] {
					seen[k] = true
					out = append(out, vu)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return vulnKey(out[i]) < vulnKey(out[j]) })
	return out, nil
}
