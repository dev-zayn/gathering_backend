// Package arch holds no code. It exists so .golangci.yml can be tested: the
// rules are hand-written paths, and a list like that rots without saying so.
package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const module = "github.com/jamaa/gathering/backend"

type config struct {
	Linters struct {
		Settings struct {
			Depguard struct {
				Rules map[string]struct {
					Files []string `yaml:"files"`
					Deny  []struct {
						Pkg  string `yaml:"pkg"`
						Desc string `yaml:"desc"`
					} `yaml:"deny"`
				} `yaml:"rules"`
			} `yaml:"depguard"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

func load(t *testing.T) config {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "..", ".golangci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var c config
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// A deny line naming a package that does not exist matches nothing. It reads as
// protection and is not.
func TestNoRuleNamesAPackageThatIsGone(t *testing.T) {
	for name, rule := range load(t).Linters.Settings.Depguard.Rules {
		for _, d := range rule.Deny {
			path, ok := strings.CutPrefix(d.Pkg, module+"/")
			if !ok {
				continue // a third-party prefix; not ours to check
			}
			if _, err := os.Stat(filepath.Join("..", "..", "..", path)); err != nil {
				t.Errorf("rule %q denies %q, which is not a directory in this\n"+
					"repository — the rule matches nothing", name, d.Pkg)
			}
		}
	}
}

// A rule whose files glob matches nothing is the same failure wearing different
// clothes: it cannot fire, so it cannot fail, so it looks like it is working.
func TestEveryRuleAppliesToSomething(t *testing.T) {
	for name, rule := range load(t).Linters.Settings.Depguard.Rules {
		matched := false
		for _, pattern := range rule.Files {
			// The globs are golangci's, anchored with **/. What matters here is
			// only the directory they end in.
			dir := strings.TrimSuffix(strings.TrimPrefix(pattern, "**/"), "/**")
			err := filepath.Walk(filepath.Join("..", "..", ".."), func(p string, info os.FileInfo, err error) error {
				if err != nil || !info.IsDir() || matched {
					return nil //nolint:nilerr // an unreadable directory is not this test's business
				}
				if strings.Contains(filepath.ToSlash(p), "node_modules") {
					return filepath.SkipDir
				}
				if ok, _ := filepath.Match(dir, strings.TrimPrefix(filepath.ToSlash(p), "../../../")); ok {
					matched = true
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if !matched {
			t.Errorf("rule %q applies to %v, and no directory here matches —\n"+
				"it guards nothing", name, rule.Files)
		}
	}
}

// The lint rule is a list of prefixes, and internal/data is a prefix of
// internal/data/enum, so it cannot say "everything under data but enum". This
// can: model's own files, read as Go, import nothing of ours except enum.
func TestModelImportsNothingButEnum(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "internal", "data", "model", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, "\"")
			if strings.HasPrefix(path, module+"/") && path != module+"/internal/data/enum" {
				t.Errorf("%s imports %s — model is the centre; it imports nothing of ours but enum",
					filepath.Base(name), path)
			}
		}
	}
}
