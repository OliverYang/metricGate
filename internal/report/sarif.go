// Package report renders run summaries. The SARIF writer is the machine
// contract: GitHub code scanning, IDEs and the future LLM feedback loop all
// consume the same document.
package report

import (
	"encoding/json"

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

// SARIF renders the summary as a SARIF 2.1.0 document.
func SARIF(s *model.RunSummary, toolVersion string, ruleMetas []rules.Meta) ([]byte, error) {
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
			Results: results,
		}},
	}
	return json.MarshalIndent(log, "", "  ")
}
