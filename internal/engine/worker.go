// Worker pool plumbing for the engine's per-file stage: bounded parallelism
// with a semaphore that keeps at most two huge parse trees alive at once.
package engine

import (
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"

	"metricgate/internal/arch"
	"metricgate/internal/clones"
	"metricgate/internal/model"
	"metricgate/internal/parser"
)

// bigFileThreshold is the source size above which a parse tree is large
// enough that concurrent big trees dominate peak memory (a 3.8MB machine-
// generated file produces a ~200MB tree).
const bigFileThreshold = 1 << 20 // 1 MB

// bigFile reports whether the file must gate on the big-file semaphore.
func bigFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() >= bigFileThreshold
}

// clampWorkers bounds the worker pool: at most 8 (tree-sitter parsers are
// memory-hungry), never zero.
func clampWorkers(n int) int {
	if n > 8 {
		return 8
	}
	if n < 1 {
		return 1
	}
	return n
}

// runWorkers parses and analyses every file on a bounded worker pool and
// returns the per-file results in completion order.
func (e *Engine) runWorkers(root string, files []string) <-chan fileResult {
	workers := clampWorkers(runtime.NumCPU())
	bigSem := make(chan struct{}, 2) // at most 2 huge trees in memory at once
	jobs := make(chan string)
	results := make(chan fileResult, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := &worker{ps: map[parser.LanguageID]*parser.Parser{}}
			for path := range jobs {
				results <- e.runOne(w, root, path, bigSem)
			}
		}()
	}
	go feedJobs(jobs, files)
	go func() {
		wg.Wait()
		close(results)
	}()
	return results
}

// runOne checks one file, gating huge trees through the big-file semaphore
// and returning their memory to the OS before the next big file.
func (e *Engine) runOne(w *worker, root, path string, bigSem chan struct{}) fileResult {
	if bigFile(path) {
		bigSem <- struct{}{}
	}
	res := e.checkFile(w, root, path)
	if bigFile(path) {
		<-bigSem
		runtime.GC()
		debug.FreeOSMemory()
	}
	return res
}

// feedJobs queues every file and closes the channel when done.
func feedJobs(jobs chan<- string, files []string) {
	for _, f := range files {
		jobs <- f
	}
	close(jobs)
}

// foldResults merges per-file results into the summary and gathers the
// whole-graph inputs: architecture files, clone fingerprints and the
// suppression map keyed by reported path.
func foldResults(summary *model.RunSummary, results <-chan fileResult, archFiles *[]arch.File, cloneFns *[]clones.Func, typeKinds map[string]arch.TypeKinds) map[string][]suppression {
	supsByFile := map[string][]suppression{}
	for res := range results {
		summary.Findings = append(summary.Findings, res.findings...)
		summary.Metrics = append(summary.Metrics, res.metrics...)
		countSuppressed(summary, res.ignored, res.ignoredByRule)
		if res.parseError != "" {
			summary.ParseErrors = append(summary.ParseErrors, res.path)
		}
		if res.archFile.Pkg != "" {
			*archFiles = append(*archFiles, res.archFile)
			k := typeKinds[res.archFile.Pkg]
			k.Total += res.types.Total
			k.Abstract += res.types.Abstract
			typeKinds[res.archFile.Pkg] = k
		}
		if res.archFile.Name != "" && len(res.sups) > 0 {
			supsByFile[res.archFile.Name] = res.sups
		}
		*cloneFns = append(*cloneFns, res.cloneFns...)
	}
	// Workers report in completion order, which varies between runs; the
	// parse-error list is part of the report, so it must not.
	sort.Strings(summary.ParseErrors)
	return supsByFile
}

// worker holds the per-goroutine parser pool; tree-sitter parsers are not
// thread-safe, so each worker lazily creates one per language it meets.
type worker struct {
	ps map[parser.LanguageID]*parser.Parser
}

func (w *worker) parserFor(id parser.LanguageID) (*parser.Parser, error) {
	if p := w.ps[id]; p != nil {
		return p, nil
	}
	p, err := parser.New(id)
	if err != nil {
		return nil, err
	}
	w.ps[id] = p
	return p, nil
}
