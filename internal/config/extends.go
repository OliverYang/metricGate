package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxExtendsDepth bounds how many documents one inheritance chain may span.
// The limit is deliberately small: a policy that has to be assembled from more
// than a handful of stacked files is a distribution mechanism (a package, a
// release), not a config file, and it belongs in the toolchain rather than in
// the loader.
const MaxExtendsDepth = 10

// extendsResult is what resolving `extends:` produced for one document.
//
// Every field is zero when the document names no parent, and that is the case
// for every policy written before inheritance existed: no parentDocs, no
// effective document, and a policy that loads, parses, hashes and reports
// exactly as it did before.
type extendsResult struct {
	// effective is the merged document, or nil when nothing was inherited.
	// It is nil rather than "the same bytes" so the caller cannot tell a
	// merged document apart from an untouched one by accident: everything
	// downstream of the nil branch is byte-identical to the old behaviour.
	effective []byte
	// parents is the chain, nearest parent first.
	parents []parentDoc
	// ownKeys are the dotted keys the leaf document itself wrote. They are
	// what makes provenance honest: a key the child overrides came from the
	// child, a key only the parent carries came from the parent, and the two
	// must never be reported as the same thing.
	ownKeys map[string]bool
}

// parentDoc is one resolved parent in a chain.
type parentDoc struct {
	path string
	keys map[string]bool
}

// extendsTarget is the only part of a policy document inheritance needs.
type extendsTarget struct {
	Extends string `yaml:"extends,omitempty"`
}

// resolveExtends resolves the inheritance a policy document declares.
//
// It is a no-op for every document without an `extends:` key, including
// documents that are not YAML mappings at all — such a document has no parent
// to name, and the regular parse is what reports the shape problem.
func resolveExtends(data []byte, sourcePath string) (extendsResult, error) {
	res := extendsResult{ownKeys: documentKeys(data)}
	target := extendsPath(data)
	if target == "" {
		return res, nil
	}
	doc, err := loadPolicyDoc(data, sourcePath, 0, nil)
	if err != nil {
		return extendsResult{}, err
	}
	res.effective = doc.data
	res.parents = doc.parents
	res.ownKeys = doc.own
	return res, nil
}

// policyDoc is one loaded document with its chain already folded in.
type policyDoc struct {
	data    []byte
	path    string
	parents []parentDoc
	// own is what this document itself wrote, kept before the merge erases
	// the distinction between "the parent's key" and "the child's key".
	own map[string]bool
}

// loadPolicyDoc loads data (whose path is used to resolve relative parents)
// and folds every ancestor under it, nearest parent first. The returned data
// is the effective document.
func loadPolicyDoc(data []byte, path string, depth int, trace []string) (policyDoc, error) {
	doc := policyDoc{data: data, path: path}
	target := extendsPath(data)
	if target == "" {
		return doc, nil
	}

	if depth >= MaxExtendsDepth {
		return doc, fmt.Errorf("policy inheritance deeper than %d documents (%s): a longer chain is a distribution mechanism, not a config file",
			MaxExtendsDepth, strings.Join(append(append([]string{}, trace...), target), " -> "))
	}
	parentPath := extendsPathFor(path, target)
	if abs := absPath(parentPath); abs != "" {
		for _, seen := range trace {
			if seen == abs {
				return doc, fmt.Errorf("policy inheritance cycle: %s -> %s", strings.Join(trace, " -> "), abs)
			}
		}
		trace = append(trace, abs)
	}
	parentData, err := os.ReadFile(parentPath)
	if err != nil {
		return doc, fmt.Errorf("%s extends %q: %v", policyLabel(path), target, err)
	}
	parent, err := loadPolicyDoc(parentData, parentPath, depth+1, trace)
	if err != nil {
		return doc, err
	}
	merged, err := mergePolicyDocs(parent.data, data)
	if err != nil {
		return doc, fmt.Errorf("%s extends %q: %v", policyLabel(path), target, err)
	}
	if merged == nil {
		// One side is not a mapping, so there is nothing to fold together.
		// Keep the child untouched and let the regular parse report the shape.
		return doc, nil
	}
	doc.data = merged
	doc.parents = append([]parentDoc{{path: parentPath, keys: parentKeys(parent)}}, parent.parents...)
	// The child's own keys travel with the document so the caller can tell an
	// override from an inheritance, which is impossible once the two are merged
	// into one node tree.
	doc.own = documentKeys(data)
	return doc, nil
}

// parentKeys is everything a parent document carries: its own keys plus every
// key its own parents contributed. It is the parent's contribution to the
// merged document, used both to attribute inherited values and to work out
// which keys in the merged document the leaf wrote itself.
func parentKeys(doc policyDoc) map[string]bool {
	out := documentKeys(doc.data)
	for _, p := range doc.parents {
		for k := range p.keys {
			out[k] = true
		}
	}
	return out
}

// extendsPath reads the top-level `extends:` value. It returns "" for a
// document that names no parent and for a document that cannot be read as a
// mapping, because "which parent" is a question only a mapping can answer.
func extendsPath(data []byte) string {
	var probe extendsTarget
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Extends)
}

// extendsPathFor resolves a parent path against the document that named it.
// An absolute target is used as written; a relative one is resolved against
// the policy's own directory so the link survives the repo being checked out
// anywhere. A document loaded from bytes rather than a file (the built-in
// default, a test, a stdin policy) resolves against the working directory.
func extendsPathFor(fromPath, target string) string {
	if filepath.IsAbs(target) {
		return target
	}
	if fromPath == "" {
		return target
	}
	return filepath.Join(filepath.Dir(fromPath), target)
}

// documentKeys collects the dotted keys of one document. A nil result means
// the document could not be parsed; the caller's own parse reports that.
func documentKeys(data []byte) map[string]bool {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	keys := map[string]bool{}
	root := doc.Content
	if len(root) > 0 {
		collectYAMLKeys(root[0], "", keys)
	}
	return keys
}

// mergePolicyDocs folds the parent document under the child and returns the
// effective document.
//
// The rules are the ones a reader can predict without learning a merge
// language: the child's keys win; two mappings under the same key merge
// recursively, so an org base policy can set a whole `languages:` block and a
// repository can override one threshold inside it; and every sequence is
// replaced wholesale by whichever document supplies it, because a silently
// concatenated allow-list is how a repository inherits permissions nobody
// asked it to take.
//
// The child's own `extends:` key is consumed here rather than carried into the
// result: the effective document has no parent, since it already contains the
// parent's content.
func mergePolicyDocs(parent, child []byte) ([]byte, error) {
	parentRoot, err := mappingRoot(parent)
	if err != nil {
		return nil, err
	}
	childRoot, err := mappingRoot(child)
	if err != nil {
		return nil, err
	}
	if parentRoot == nil || childRoot == nil {
		return nil, nil
	}
	merged := mergedMapping(parentRoot, childRoot)
	out, err := yaml.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("render merged policy: %w", err)
	}
	return out, nil
}

// mappingRoot returns the root mapping of a document, or nil when the document
// is not one.
func mappingRoot(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	root := doc.Content
	if len(root) == 0 || root[0].Kind != yaml.MappingNode {
		return nil, nil
	}
	return root[0], nil
}

// mergedMapping overlays child on parent, keeping the parent's key order and
// appending the child's new keys, so the same inputs always render the same
// effective document.
func mergedMapping(parent, child *yaml.Node) *yaml.Node {
	out := &yaml.Node{
		Kind:        yaml.MappingNode,
		Tag:         child.Tag,
		Style:       child.Style,
		HeadComment: child.HeadComment,
		LineComment: child.LineComment,
	}
	childAt := make(map[string]int, len(child.Content)/2)
	for i := 0; i+1 < len(child.Content); i += 2 {
		childAt[child.Content[i].Value] = i
	}
	overridden := map[string]bool{}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		key, value := parent.Content[i], parent.Content[i+1]
		j, ok := childAt[key.Value]
		if !ok {
			out.Content = append(out.Content, key, value)
			continue
		}
		overridden[key.Value] = true
		out.Content = append(out.Content, child.Content[j], mergedValue(value, child.Content[j+1]))
	}
	for j := 0; j+1 < len(child.Content); j += 2 {
		key := child.Content[j]
		if key.Value == "extends" || overridden[key.Value] {
			continue
		}
		out.Content = append(out.Content, key, child.Content[j+1])
	}
	return out
}

// mergedValue decides what a key the child supplies means. Two mappings become
// one merged mapping; anything else is the child's, replacing the parent's.
func mergedValue(parent, child *yaml.Node) *yaml.Node {
	if parent.Kind == yaml.MappingNode && child.Kind == yaml.MappingNode {
		return mergedMapping(parent, child)
	}
	return child
}

// policyLabel names a policy document in error messages, falling back to a
// description when the document came from bytes rather than a file.
func policyLabel(path string) string {
	if path == "" {
		return "policy"
	}
	return path
}

// absPath returns the absolute form of a path without requiring it to exist,
// so a cycle can be recognised before the read is attempted. It returns "" for
// a path the platform cannot absolutise.
func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return abs
}
