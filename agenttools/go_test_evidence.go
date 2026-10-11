package agenttools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

type goTestArguments struct {
	Package string   `json:"package"`
	Tags    []string `json:"tags,omitempty"`
}

const goTestEvidenceToolGuidance = " Optional tags admits only mongo_integration for temporary local fixtures, never live qualification or soak runs. test_evidence records observed PASS/FAIL/SKIP and excluded NOT_RUN tests. Package-level ok alone does not prove tagged tests executed. A validation PASS citing a test must have its observed passing event; enable the qualified tag and run it or report INCONCLUSIVE."

var goTestName = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)

func (a goTestArguments) valid() bool {
	if len(a.Tags) > 1 {
		return false
	}
	// Repository-wide qualified mode; live qualification/soak tags are not
	// agent authority. This is independent of any feature's test names.
	for _, tag := range a.Tags {
		if tag != "mongo_integration" {
			return false
		}
	}
	return true
}

type GoTestCase struct {
	Package string `json:"package"`
	Name    string `json:"name"`
	Status  string `json:"status"`
}

// A package-level ok is not proof that a tagged or skipped test executed.
type GoTestEvidence struct {
	Command  []string     `json:"command"`
	Tags     []string     `json:"tags,omitempty"`
	Tests    []GoTestCase `json:"tests"`
	Excluded []GoTestCase `json:"excluded"`
	Complete bool         `json:"complete"`
}

// Consume Go's -json events and retain conventional human-readable output.
// Evidence and buffering are finite; overflow never implies complete coverage.
type goTestObservations struct {
	output   io.Writer
	pending  []byte
	tests    map[string]GoTestCase
	complete bool
}

func (w *goTestObservations) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.pending)+n > 2<<20 {
		w.complete = false
		w.pending = nil
		return n, nil
	}
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := w.pending[:i]
		w.pending = w.pending[i+1:]
		var e struct{ Action, Package, Test, Output string }
		if json.Unmarshal(line, &e) != nil || e.Action == "" {
			w.complete = false
			if _, err := w.output.Write(append(bytes.Clone(line), '\n')); err != nil {
				return n, err
			}
			continue
		}
		if e.Output != "" && !strings.HasPrefix(e.Output, "=== ") && !strings.HasPrefix(strings.TrimSpace(e.Output), "--- PASS:") {
			if _, err := io.WriteString(w.output, e.Output); err != nil {
				return n, err
			}
		}
		if e.Test != "" {
			if len(e.Package) > 1024 || len(e.Test) > 1024 {
				w.complete = false
				continue
			}
			key := e.Package + "\x00" + e.Test
			if len(w.tests) >= 8192 {
				if _, found := w.tests[key]; !found {
					w.complete = false
					continue
				}
			}
			c := w.tests[key]
			c.Package, c.Name = e.Package, e.Test
			switch e.Action {
			case "run":
				c.Status = "RUNNING"
			case "pass", "fail", "skip":
				if c.Status == "" {
					w.complete = false
				}
				c.Status = strings.ToUpper(e.Action)
			}
			w.tests[key] = c
		}
	}
	return n, nil
}

func (w *goTestObservations) finish(e *GoTestEvidence) {
	e.Complete = w.complete && len(bytes.TrimSpace(w.pending)) == 0
	e.Tests = e.Tests[:0]
	for _, c := range w.tests {
		if c.Status == "RUNNING" {
			e.Complete = false
		}
		e.Tests = append(e.Tests, c)
	}
	slices.SortFunc(e.Tests, func(a, b GoTestCase) int { return strings.Compare(a.Package+"\x00"+a.Name, b.Package+"\x00"+b.Name) })
}

func excludedGoTests(data []byte) ([]GoTestCase, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	result := []GoTestCase{}
	for {
		var p struct {
			Dir, ImportPath string
			IgnoredGoFiles  []string
		}
		err := d.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, name := range p.IgnoredGoFiles {
			if !strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(p.Dir, name), nil, 0)
			if err != nil {
				return nil, fmt.Errorf("excluded test inventory: %w", err)
			}
			for _, decl := range file.Decls {
				if f, ok := decl.(*ast.FuncDecl); ok && f.Recv == nil && goTestName.MatchString(f.Name.Name) && f.Name.Name != "TestMain" {
					if len(result) >= 8192 {
						return nil, ErrTooLarge
					}
					result = append(result, GoTestCase{Package: p.ImportPath, Name: f.Name.Name, Status: "NOT_RUN"})
				}
			}
		}
	}
	return result, nil
}
