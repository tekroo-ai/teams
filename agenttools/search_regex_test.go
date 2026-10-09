package agenttools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestSearchRegexLargerRepeatRetainsExactBounds(t *testing.T) {
	for _, pattern := range []string{`^a{2000}$`, `^a{1999,2000}$`, `^a{2000,}$`, `^[[:alpha:]]{2000}$`, `^\x{61}{2000}$`, `^\Qa\E{2000}$`, `^(?i:a){2000}$`, `^(?P<letter>a){2000}$`} {
		re, err := compileSearchRegex(pattern)
		if err != nil || !re.MatchString(strings.Repeat("a", 2000)) || re.MatchString(strings.Repeat("a", 1998)) {
			t.Fatalf("%q: %v", pattern, err)
		}
		if !strings.Contains(pattern, ",}") && re.MatchString(strings.Repeat("a", 2001)) {
			t.Fatalf("broadened %q", pattern)
		}
	}
	for _, pattern := range []string{`a*{2000}`, `(a{2000}){2000}`, `a{4097}`, `a{2000,1001}`, `(?=a)a{2000}`, `(a)\1{2000}`, `[a{2000`, `a{2000}**`} {
		if _, err := compileSearchRegex(pattern); err == nil {
			t.Fatalf("invalid/unbounded-complexity regex accepted: %q", pattern)
		}
	}
}

func TestRepeatExpansionMatchesStandardRE2Locations(t *testing.T) {
	patterns := []string{`(a|ab){0,4}b`, `(ab|a){1,4}?b?`, `(a{1,2}|b){0,3}`, `(?i)[a-z]{2,4}`, `\Qab\E{2,3}`, `\x61{0,4}`, `\p{Greek}{1,3}`, `[]{]a{0,2}`, `[[:alpha:]]{0,3}`, `(?:a?){0,3}`, `a{0,0}`, `a{2,}`, `(?U)a{1,4}b?`}
	texts := []string{"", "a", "b", "ab", "aba", "abb", "abab", "ababa", "aab", "aaaaab", "abc", "AAab", "αβγδ", "{}aaa", "ab\nab", "xaaaabx"}
	for _, pattern := range patterns {
		standard, err := regexp.Compile(pattern)
		if err != nil {
			t.Fatal(pattern, err)
		}
		expanded, _, err := expandSearchRegex(pattern, 0)
		if err != nil {
			t.Fatal(pattern, err)
		}
		re, err := regexp.Compile(expanded)
		if err != nil {
			t.Fatal(expanded, err)
		}
		for _, text := range texts {
			if !reflect.DeepEqual(standard.FindAllStringIndex(text, -1), re.FindAllStringIndex(text, -1)) {
				t.Fatalf("%s => %s on %q: %v != %v", pattern, expanded, text, standard.FindAllStringIndex(text, -1), re.FindAllStringIndex(text, -1))
			}
		}
	}
}

func TestSearchRegexMultilineIsExplicitBoundedAndLocated(t *testing.T) {
	root := t.TempDir()
	body := "prefix\nfunc validActorOperand() {\n" + strings.Repeat("a", 1500) + "\n}\nsuffix\nfunc validActorOperand() {\n}\n"
	if err := os.WriteFile(filepath.Join(root, "handler.go"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	authority := Authority{WorkspaceRoot: root, Permissions: []string{"repository.read"}}
	args := map[string]any{"regex": `func validActorOperand\([^)]*\)\s*\{[\s\S]{0,2000}?\}`, "path": "handler.go"}
	invoke := func() Result {
		t.Helper()
		encoded, _ := json.Marshal(args)
		result, err := testHost().execute(t.Context(), authority, call("search_regex", string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := invoke(); len(result.Matches) != 0 {
		t.Fatal("default search crossed lines", result)
	}
	args["multiline"] = true
	args["after_lines"] = 1
	result := invoke()
	if len(result.Matches) != 2 || result.Matches[0].Line != 2 || result.Matches[0].EndLine != 4 || result.Matches[0].After[0] != "suffix" {
		t.Fatalf("%+v", result)
	}
	delete(args, "after_lines")
	args["result_mode"] = "count"
	if result := invoke(); result.MatchCount == nil || *result.MatchCount != 2 {
		t.Fatalf("%+v", result)
	}
	args["result_mode"] = "lines"
	args["max_results"] = 1
	if result := invoke(); len(result.Matches) != 1 || !result.Truncated {
		t.Fatalf("%+v", result)
	}
	if err := os.WriteFile(filepath.Join(root, "large.go"), []byte(strings.Repeat("a", (256<<10)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	args["path"] = "large.go"
	if result := invoke(); result.SkippedFiles != 1 || !result.Truncated {
		t.Fatalf("silent oversized-file absence: %+v", result)
	}
}

func BenchmarkSearchRegexRepeat2000(b *testing.B) {
	re, err := compileSearchRegex(`^a{0,2000}z$`)
	if err != nil {
		b.Fatal(err)
	}
	text := strings.Repeat("a", 2000) + "z"
	if !re.MatchString(text) {
		b.Fatal("missing warm-up match")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !re.MatchString(text) {
			b.Fatal("missing match")
		}
	}
}

func FuzzRepeatExpansionLocations(f *testing.F) {
	for _, pattern := range []string{`(a|ab){0,4}b`, `(?i:a){1,3}`, `\Qab\E{2,3}`, `(?U)a{1,4}b?`, `a{0,0}`, `(?:a?){0,3}`} {
		f.Add(pattern, "aababa")
	}
	f.Fuzz(func(t *testing.T, pattern, text string) {
		if len(pattern) > 64 || len(text) > 128 {
			return
		}
		standard, err := regexp.Compile(pattern)
		if err != nil {
			return
		}
		expanded, _, err := expandSearchRegex(pattern, 0)
		if err != nil {
			return // Explicit bounded complexity rejection is permitted.
		}
		re, err := regexp.Compile(expanded)
		if err != nil {
			t.Fatalf("valid pattern %q expanded into invalid %q: %v", pattern, expanded, err)
		}
		if !reflect.DeepEqual(standard.FindAllStringIndex(text, -1), re.FindAllStringIndex(text, -1)) {
			t.Fatalf("%q => %q on %q: %v != %v", pattern, expanded, text, standard.FindAllStringIndex(text, -1), re.FindAllStringIndex(text, -1))
		}
	})
}
