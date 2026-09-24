package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"metricgate/internal/selection"
)

// PreviewReport is the deterministic, read-only selection explanation for a
// root. It reports repo-relative paths; pruned directories stand for every
// descendant omitted by the existing directory-name exclude policy.
type PreviewReport struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Root          string               `json:"root"`
	Candidate     int                  `json:"candidateFiles"`
	Included      []selection.Decision `json:"included"`
	Excluded      []selection.Decision `json:"excluded"`
	Unsupported   []selection.Decision `json:"unsupported"`
	PrunedDirs    []selection.Decision `json:"prunedDirectories"`
}

type selectedFile struct {
	decision selection.Decision
	absolute string
}

// Preview computes the same path decisions consumed by Run without parsing
// files, running rules, touching the trend store, or invoking git.
func (e *Engine) Preview(root string) (*PreviewReport, error) {
	report, _, err := e.scanSelection(root)
	if err != nil {
		return nil, err
	}
	return report, nil
}

// scanSelection owns filesystem traversal; the path semantics themselves
// live in internal/selection.Decide and are shared with the facts adapter.
func (e *Engine) scanSelection(root string) (*PreviewReport, []selectedFile, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, nil, err
	}
	report := &PreviewReport{SchemaVersion: 1, Root: filepath.ToSlash(root)}
	var selected []selectedFile
	if !info.IsDir() {
		path := filepath.Base(root)
		d := selection.Decide(path, false, e.cfg.Exclude)
		report.Candidate = 1
		appendDecision(report, d)
		if d.Status == selection.Included {
			selected = append(selected, selectedFile{decision: d, absolute: root})
		}
		return report, selected, nil
	}

	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil // preserve existing semantics: root itself is never excluded
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			d := selection.Decide(rel, true, e.cfg.Exclude)
			if d.Status == selection.Excluded {
				report.PrunedDirs = append(report.PrunedDirs, d)
				return filepath.SkipDir
			}
			return nil
		}
		report.Candidate++
		d := selection.Decide(rel, false, e.cfg.Exclude)
		appendDecision(report, d)
		if d.Status == selection.Included {
			selected = append(selected, selectedFile{decision: d, absolute: path})
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(report.PrunedDirs, func(i, j int) bool { return report.PrunedDirs[i].Path < report.PrunedDirs[j].Path })
	sort.Slice(selected, func(i, j int) bool { return selected[i].decision.Path < selected[j].decision.Path })
	return report, selected, nil
}

func appendDecision(r *PreviewReport, d selection.Decision) {
	switch d.Status {
	case selection.Included:
		r.Included = append(r.Included, d)
	case selection.Excluded:
		r.Excluded = append(r.Excluded, d)
	case selection.Unsupported:
		r.Unsupported = append(r.Unsupported, d)
	}
}
