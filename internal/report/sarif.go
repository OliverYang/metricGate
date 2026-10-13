// Package report renders run summaries. The SARIF writer is the machine
// contract: GitHub code scanning, IDEs and the future LLM feedback loop all
// consume the same document.
package report

import (
	"encoding/json"
	"strings"

	"metricgate/internal/bundle"
	"metricgate/internal/config"
	"metricgate/internal/model"
	"metricgate/internal/rules"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
	// Properties carries the run-level provenance that made the document
	// auditable after the fact: which policy version produced these verdicts.
	// Without it, two uploads of an old and a new commit are indistinguishable,
	// so a dismissal on one says nothing about the other.
	Properties *sarifRunProps `json:"properties,omitempty"`
}

type sarifRunProps struct {
	PolicyDigest string `json:"policyDigest,omitempty"`
	PolicySource string `json:"policySource,omitempty"`
	// TraceID is the ambient trace, the key that ties this upload to the tool
	// calls and runs the same unit of work produced. It is empty for a
	// hand-run check, which is a legal state rather than missing data.
	TraceID string `json:"traceId,omitempty"`
	// RunID is the row this run occupies in the history store, so an uploaded
	// SARIF can be traced back to the run that produced it without a second
	// lookup. Empty when the store is disabled, which is a legal state.
	RunID string `json:"runId,omitempty"`
	// Extends names the parent documents the policy inherited from, so a
	// reader of the upload can tell an inherited threshold from a repository's
	// own. Comma-separated, nearest parent first.
	Extends string `json:"extends,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string    `json:"id"`
	ShortDescription sarifText `json:"shortDescription"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID     string     `json:"ruleId"`
	Level      string     `json:"level"`
	Message    sarifText  `json:"message"`
	Locations  []sarifLoc `json:"locations"`
	Properties sarifProps `json:"properties"`
}

type sarifProps struct {
	Source model.Source `json:"source"`
	// Evidence carries the deterministic anchor (byte span + snippet hash)
	// inside result.properties, so it never alters the SARIF locations that
	// editors and GitHub code scanning rely on.
	Evidence *model.Evidence `json:"evidence,omitempty"`
}

type sarifLoc struct {
	Physical sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	Artifact sarifArtifact `json:"artifactLocation"`
	Region   sarifRegion   `json:"region"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
}

// SARIF renders the summary as a SARIF 2.1.0 document. cfg is optional:
// passing nil omits the run properties rather than emitting an empty object.
func SARIF(s *model.RunSummary, toolVersion string, ruleMetas []rules.Meta, cfg *config.Config) ([]byte, error) {
	return SARIFWithRun(s, toolVersion, ruleMetas, cfg, "")
}

// SARIFWithRun is SARIF with the run id stamped on.
//
// The run id is the one provenance field the report layer cannot know on its
// own — the store assigns it while writing the row this document describes —
// so it arrives as an argument, and an empty one means "this document is not
// tied to a stored run", which is a legal state rather than missing data.
func SARIFWithRun(s *model.RunSummary, toolVersion string, ruleMetas []rules.Meta, cfg *config.Config, runID string) ([]byte, error) {
	metas := map[string]string{}
	for _, m := range ruleMetas {
		metas[m.ID] = m.Title
	}
	// Metric findings have no entry in the rule registry; add them so every
	// result has a rule definition.
	for _, f := range s.Findings {
		if _, ok := metas[f.RuleID]; !ok {
			metas[f.RuleID] = "Metric threshold exceeded"
		}
	}

	ruleList := make([]sarifRule, 0, len(metas))
	for id, title := range metas {
		ruleList = append(ruleList, sarifRule{ID: id, ShortDescription: sarifText{Text: title}})
	}

	results := make([]sarifResult, 0, len(s.Findings))
	for _, f := range s.Findings {
		sc := f.StartCol
		if sc < 1 {
			sc = 1
		}
		ec := f.EndCol
		if ec < 1 {
			ec = sc
		}
		results = append(results, sarifResult{
			RuleID:  f.RuleID,
			Level:   f.Severity.SARIFLevel(),
			Message: sarifText{Text: f.Message},
			Locations: []sarifLoc{{
				Physical: sarifPhysical{
					Artifact: sarifArtifact{URI: f.File},
					Region: sarifRegion{
						StartLine: f.StartLine, StartColumn: sc,
						EndLine: f.EndLine, EndColumn: ec,
					},
				},
			}},
			Properties: sarifProps{Source: f.Source, Evidence: f.Evidence},
		})
	}

	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "metricgate",
				Version:        toolVersion,
				InformationURI: "https://github.com/metricgate/metricgate",
				Rules:          ruleList,
			}},
			Results:    results,
			Properties: sarifRunProperties(cfg, runID),
		}},
	}
	return json.MarshalIndent(log, "", "  ")
}

// sarifRunProperties stamps the policy and run provenance onto the run. GitHub
// code scanning and IDEs keep run-level properties per upload, which is what
// makes "this finding was dismissed under policy X" a fact rather than a
// memory, and what lets an uploaded document be traced back to the run and
// the unit of work that produced it without a second lookup.
func sarifRunProperties(cfg *config.Config, runID string) *sarifRunProps {
	if cfg == nil {
		return nil
	}
	return &sarifRunProps{
		PolicyDigest: bundle.PolicyDigest(cfg),
		PolicySource: bundle.PolicySourceLabel(cfg),
		TraceID:      traceID(),
		RunID:        runID,
		Extends:      strings.Join(extendsPaths(cfg), ","),
	}
}
