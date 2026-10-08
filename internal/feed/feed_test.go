package feed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const log4shellJSON = `{
  "id": "GHSA-jfh8-c2jp-5v3q",
  "aliases": ["CVE-2021-44228"],
  "summary": "Apache Log4j2 allows RCE via JNDI",
  "affected": [
    {"package": {"ecosystem": "Maven", "name": "org.apache.logging.log4j:log4j-core"},
     "ranges": [{"type": "ECOSYSTEM", "events": [{"introduced": "2.0-beta9"}, {"fixed": "2.15.0"}]}]},
    {"package": {"ecosystem": "npm", "name": "some-npm-pkg"},
     "ranges": [{"type": "SEMVER", "events": [{"introduced": "0"}]}]}
  ]
}`

func mustParse(t *testing.T, data string) []Vuln {
	t.Helper()
	vs, err := ParseOSV([]byte(data))
	if err != nil {
		t.Fatalf("ParseOSV: %v", err)
	}
	return vs
}

func TestParseOSVListAndSingle(t *testing.T) {
	list := mustParse(t, `{"vulns":[`+log4shellJSON+`]}`)
	if len(list) != 2 { // two affected packages, one unsupported
		t.Fatalf("list: got %d vulns, want 2", len(list))
	}
	single := mustParse(t, log4shellJSON)
	if len(single) != 2 {
		t.Fatalf("single: got %d vulns, want 2", len(single))
	}
	v := list[len(list)-1] // sort puts Lang=="" first; the java entry is last
	if v.Lang != "java" || v.Match != "org.apache.logging.log4j" || v.CVE != "CVE-2021-44228" {
		t.Fatalf("derive wrong: %+v", v)
	}
	if v.Introduced != "2.0-beta9" || v.Fixed != "2.15.0" {
		t.Fatalf("range events lost: %+v", v)
	}
	foundNPM := false
	for _, x := range list {
		if x.Ecosystem == "npm" && x.Lang == "" {
			foundNPM = true
		}
	}
	if !foundNPM {
		t.Fatalf("npm package must survive with Lang=\"\" for the ledger")
	}
}

func TestPlanDedupAndSkip(t *testing.T) {
	vs := mustParse(t, `{"vulns":[`+log4shellJSON+`]}`)
	existing := map[string]bool{"java\x00import\x00org.apache.logging.log4j": true}
	res, groups := PlanGroups(vs, existing)
	if len(res.Added) != 0 || len(res.Existing) != 1 || len(res.Skipped) != 1 {
		t.Fatalf("plan: added=%d existing=%d skipped=%d", len(res.Added), len(res.Existing), len(res.Skipped))
	}
	if len(groups) != 0 {
		t.Fatalf("fully-deduped feed must produce no groups")
	}
	res2, groups2 := PlanGroups(vs, map[string]bool{})
	if len(res2.Added) != 1 || len(res2.Skipped) != 1 || len(groups2) != 1 || groups2[0].Lang != "java" {
		t.Fatalf("plan2: %+v groups=%v", res2, groups2)
	}
}

const basePolicy = `project: demo
languages:
  go:
    banned-api:
      - match: fmt.Print*
        reason: repo standard
  java:
    banned-api:
      - match: System.out.print*
        reason: repo standard
`

// mergeToMap merges groups into basePolicy and unmarshals the result so the
// test asserts on parsed structure, not string shapes.
func mergeToMap(t *testing.T, groups []LangGroup) map[string]any {
	t.Helper()
	out, err := Merge([]byte(basePolicy), groups)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("merged output does not parse: %v\n%s", err, out)
	}
	return m
}

func TestMergeIntoExistingSection(t *testing.T) {
	vs := mustParse(t, `{"vulns":[`+log4shellJSON+`]}`)
	res, groups := PlanGroups(vs, map[string]bool{})
	if len(res.Added) != 1 {
		t.Fatalf("want 1 added, got %d", len(res.Added))
	}
	m := mergeToMap(t, groups)
	langs := m["languages"].(map[string]any)
	java := langs["java"].(map[string]any)
	bans := java["banned-api"].([]any)
	if len(bans) != 2 {
		t.Fatalf("java bans: got %d, want 2", len(bans))
	}
	entry := bans[1].(map[string]any)
	if entry["match"] != "org.apache.logging.log4j" || entry["kind"] != "import" {
		t.Fatalf("merged entry wrong: %v", entry)
	}
	reason := entry["reason"].(string)
	if !strings.Contains(reason, "CVE-2021-44228") || !strings.Contains(reason, "tripwire") {
		t.Fatalf("reason lost advisory identity: %s", reason)
	}
	// untouched siblings survive verbatim
	if len(langs["go"].(map[string]any)["banned-api"].([]any)) != 1 {
		t.Fatalf("go section was disturbed")
	}
}

func TestMergeCreatesMissingSection(t *testing.T) {
	groups := []LangGroup{{Lang: "kotlin", Rows: []BanRow{{Kind: "import", Match: "evil.pkg", Reason: "test", Use: "use ok.pkg"}}}}
	m := mergeToMap(t, groups)
	langs := m["languages"].(map[string]any)
	kotlin, ok := langs["kotlin"].(map[string]any)
	if !ok {
		t.Fatalf("kotlin section missing: %v", langs)
	}
	if len(kotlin["banned-api"].([]any)) != 1 {
		t.Fatalf("kotlin bans wrong")
	}
}

func TestMergeJavaSectionWithoutBannedKey(t *testing.T) {
	noBans := "languages:\n  java:\n    metrics:\n      cyclomatic-complexity: {warn: 15, error: 20}\n"
	out, err := Merge([]byte(noBans), []LangGroup{{Lang: "java", Rows: []BanRow{{Kind: "import", Match: "x.y", Reason: "r"}}}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("no-key merge unparseable: %v\n%s", err, out)
	}
	java := m["languages"].(map[string]any)["java"].(map[string]any)
	if len(java["banned-api"].([]any)) != 1 {
		t.Fatalf("banned-api not inserted: %v", java)
	}
	if _, ok := java["metrics"]; !ok {
		t.Fatalf("sibling key disturbed: %v", java)
	}
}

func TestMergeEmptyPolicyAppendsBlock(t *testing.T) {
	out, err := Merge([]byte("# just a comment\n"), []LangGroup{{Lang: "go", Rows: []BanRow{{Kind: "import", Match: "evil", Reason: "r"}}}})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("empty-base merge unparseable: %v\n%s", err, out)
	}
	if m["languages"] == nil {
		t.Fatalf("languages key not created:\n%s", out)
	}
}

func TestMergeDeterminism(t *testing.T) {
	g := []LangGroup{{Lang: "java", Rows: []BanRow{{Kind: "import", Match: "a.b", Reason: "r"}}}}
	o1, err := Merge([]byte(basePolicy), g)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	o2, _ := Merge([]byte(basePolicy), g)
	if o1 != o2 {
		t.Fatalf("merge is not deterministic")
	}
}

func TestDedupKeysDefaultsToCallKind(t *testing.T) {
	keys := DedupKeys(map[string][]BanRow{"go": {{Match: "db.Query"}}})
	if !keys["go\x00call\x00db.Query"] {
		t.Fatalf("empty kind must default to call: %v", keys)
	}
}

func TestRoundTripSmokeOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "osv.json")
	if err := os.WriteFile(path, []byte(log4shellJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if vs := mustParse(t, string(data)); len(vs) != 2 {
		t.Fatalf("disk round-trip: %d", len(vs))
	}
}

const scannerReportJSON = `{
  "results": [
    {"source": {"path": "/x/pom.xml", "type": "lockfile"},
     "packages": [
       {"package": {"name": "org.apache.logging.log4j:log4j-core", "version": "2.14.1", "ecosystem": "Maven"},
        "vulnerabilities": [{"id": "GHSA-jfh8-c2jp-5v3q", "aliases": ["CVE-2021-44228"],
          "summary": "Apache Log4j2 allows RCE via JNDI",
          "affected": [{"package": {"ecosystem": "Maven", "name": "org.apache.logging.log4j:log4j-core"},
            "ranges": [{"type": "ECOSYSTEM", "events": [{"introduced": "2.0-beta9"}, {"fixed": "2.15.0"}]}]}]},
       {"id": "GHSA-dup-0001", "aliases": []}]},
       {"package": {"name": "left-pad", "version": "1.3.0", "ecosystem": "npm"},
        "vulnerabilities": [{"id": "GHSA-npm-0001", "aliases": ["CVE-2016-0001"]}]}
     ]},
    {"source": {"path": "/y/go.mod", "type": "lockfile"},
     "packages": [
       {"package": {"name": "github.com/gogo/protobuf", "version": "1.3.1", "ecosystem": "Go"},
        "vulnerabilities": [{"id": "GHSA-c3h9-896r-86jm", "aliases": ["CVE-2021-3121"]}]}
     ]}
  ]
}`

func TestParseScanReportFlattensPackages(t *testing.T) {
	vs, err := ParseScanReport([]byte(scannerReportJSON))
	if err != nil {
		t.Fatalf("ParseScanReport: %v", err)
	}
	// same vuln from several sources collapses; npm lands as a ledger entry
	if len(vs) != 4 {
		t.Fatalf("got %d vulns, want 4 (log4j, dup, gogo, npm)", len(vs))
	}
	byID := map[string]Vuln{}
	for _, v := range vs {
		byID[v.ID] = v
	}
	l := byID["GHSA-jfh8-c2jp-5v3q"]
	if l.Lang != "java" || l.Match != "org.apache.logging.log4j" || l.CVE != "CVE-2021-44228" || l.Fixed != "2.15.0" {
		t.Fatalf("maven entry wrong: %+v", l)
	}
	g := byID["GHSA-c3h9-896r-86jm"]
	if g.Lang != "go" || g.Match != "github.com/gogo/protobuf" {
		t.Fatalf("go entry wrong: %+v", g)
	}
	if byID["GHSA-npm-0001"].Lang != "" {
		t.Fatalf("npm entry must be a ledger row: %+v", byID["GHSA-npm-0001"])
	}
}

func TestParseScanReportRejectsGarbage(t *testing.T) {
	if _, err := ParseScanReport([]byte("not json")); err == nil {
		t.Fatal("garbage must error")
	}
	if _, err := ParseOSV([]byte(`{"results": []}`)); err == nil {
		t.Fatal("a scanner report is not an OSV advisory — ParseOSV must error")
	}
}

func TestParseAnySniffsScannerReport(t *testing.T) {
	vs, err := ParseAny([]byte(scannerReportJSON))
	if err != nil || len(vs) != 4 {
		t.Fatalf("ParseAny on scanner report: %v, %d", err, len(vs))
	}
	vs2, err := ParseAny([]byte(log4shellJSON))
	if err != nil || len(vs2) != 2 {
		t.Fatalf("ParseAny on OSV advisory: %v, %d", err, len(vs2))
	}
}
