package fsnotify

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"
)

const recursiveExceptionPolicyPath = ".github/policy/recursive-platform-exceptions.json"

type recursiveExceptionPolicy struct {
	Version    int                          `json:"version"`
	Exceptions []recursivePlatformException `json:"exceptions"`
}

type recursivePlatformException struct {
	ID                   string   `json:"id"`
	Category             string   `json:"category"`
	Guard                string   `json:"guard"`
	Platforms            []string `json:"platforms"`
	Contracts            []string `json:"contracts"`
	Reason               string   `json:"reason"`
	NonRecursiveEvidence []string `json:"non_recursive_evidence"`
	Documentation        []string `json:"documentation"`
	Audit                string   `json:"audit"`
}

func loadRecursiveExceptionPolicy(t *testing.T) recursiveExceptionPolicy {
	t.Helper()

	data, err := os.ReadFile(recursiveExceptionPolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	var policy recursiveExceptionPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestRecursiveExceptionPolicy(t *testing.T) {
	policy := loadRecursiveExceptionPolicy(t)
	if policy.Version != 1 {
		t.Fatalf("policy version = %d; want 1", policy.Version)
	}

	contract, err := os.ReadFile("RECURSIVE_WATCH_CONTRACT.md")
	if err != nil {
		t.Fatal(err)
	}
	audit, err := os.ReadFile("QUALITY_AUDIT.md")
	if err != nil {
		t.Fatal(err)
	}

	allowedCategories := []string{
		"NATIVE_CAPABILITY",
		"NATIVE_EVENT",
		"TEST_ENVIRONMENT",
	}
	ids := make(map[string]struct{}, len(policy.Exceptions))
	guards := make(map[string]string, len(policy.Exceptions))
	previousID := ""
	for _, exception := range policy.Exceptions {
		if exception.ID == "" {
			t.Fatal("exception has empty id")
		}
		if previousID != "" && exception.ID < previousID {
			t.Fatalf("exceptions are not sorted by id: %q precedes %q", previousID, exception.ID)
		}
		previousID = exception.ID
		if _, exists := ids[exception.ID]; exists {
			t.Fatalf("duplicate exception id %q", exception.ID)
		}
		ids[exception.ID] = struct{}{}

		if !slices.Contains(allowedCategories, exception.Category) {
			t.Errorf("%s: invalid category %q", exception.ID, exception.Category)
		}
		if exception.Guard == "" {
			t.Errorf("%s: guard is empty", exception.ID)
		} else if existing, exists := guards[exception.Guard]; exists {
			t.Errorf("%s and %s use the same guard %q", existing, exception.ID, exception.Guard)
		} else {
			guards[exception.Guard] = exception.ID
		}
		if len(exception.Platforms) == 0 {
			t.Errorf("%s: platforms is empty", exception.ID)
		}
		for _, platform := range exception.Platforms {
			if !isKnownPlatformSelector(platform) {
				t.Errorf("%s: unknown platform/backend selector %q", exception.ID, platform)
			}
		}
		if len(exception.Contracts) == 0 {
			t.Errorf("%s: contracts is empty", exception.ID)
		}
		if strings.TrimSpace(exception.Reason) == "" {
			t.Errorf("%s: reason is empty", exception.ID)
		}
		if len(exception.NonRecursiveEvidence) == 0 {
			t.Errorf("%s: non_recursive_evidence is empty", exception.ID)
		}
		if len(exception.Documentation) == 0 {
			t.Errorf("%s: documentation is empty", exception.ID)
		}
		if !regexp.MustCompile(`^AUDIT-TEST-[0-9]{3}$`).MatchString(exception.Audit) {
			t.Errorf("%s: malformed or non-test audit finding %q", exception.ID, exception.Audit)
		} else if !strings.Contains(string(audit), "### "+exception.Audit+" |") {
			t.Errorf("%s: audit finding %q does not exist", exception.ID, exception.Audit)
		}

		for _, rc := range exception.Contracts {
			if !regexp.MustCompile(`^RC-[0-9]{3}$`).MatchString(rc) {
				t.Errorf("%s: malformed contract id %q", exception.ID, rc)
				continue
			}
			if !strings.Contains(string(contract), "### "+rc+":") {
				t.Errorf("%s: contract %q does not exist", exception.ID, rc)
			}
			if rc != "RC-024" && rc != "RC-027" {
				t.Errorf("%s: recursive control rule %q cannot be allowlisted", exception.ID, rc)
			}
		}
		for _, evidence := range exception.NonRecursiveEvidence {
			validateNonRecursiveEvidenceReference(t, exception.ID, evidence)
		}
		for _, document := range exception.Documentation {
			if _, err := os.Stat(document); err != nil {
				t.Errorf("%s: documentation %q: %v", exception.ID, document, err)
			}
		}
	}

	discovered := discoverRecursiveExceptionGuards(t)
	for guard, location := range discovered {
		if _, ok := guards[guard]; !ok {
			t.Errorf("unclassified recursive platform difference %q at %s", guard, location)
		}
	}
	for guard, id := range guards {
		if _, ok := discovered[guard]; !ok {
			t.Errorf("%s: allowlist entry is unused: %q", id, guard)
		}
	}
}

func TestRecursiveNativeEvidence(t *testing.T) {
	policy := loadRecursiveExceptionPolicy(t)

	for _, exception := range policy.Exceptions {
		if !platformListMatches(exception.Platforms, runtime.GOOS) {
			continue
		}
		for _, evidence := range exception.NonRecursiveEvidence {
			evidence := evidence
			t.Run(exception.ID+"/"+sanitizeEvidenceName(evidence), func(t *testing.T) {
				runNonRecursiveEvidence(t, evidence)
			})
		}
	}
}

func TestNonRecursiveWindowsAncestorRenameBlocked(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific non-recursive evidence")
	}
	proveNonRecursiveWindowsAncestorRenameBlocked(t)
}

func TestRecursiveControlContractHasNoPlatformBranches(t *testing.T) {
	data, err := os.ReadFile("recursive_contract_test.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"recursive_contract_test.go",
		data,
		parser.SkipObjectResolution,
	)
	if err != nil {
		t.Fatal(err)
	}
	if file.Name.Name != "fsnotify_test" {
		t.Error("recursive_contract_test.go must use package fsnotify_test and public API only")
	}

	source := string(data)
	for _, forbidden := range []string{
		"runtime.GOOS",
		"runtime.GOARCH",
		"t.Skip(",
		"t.Skipf(",
		"supportsRecurse",
		".b.",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("recursive_contract_test.go contains forbidden backend/platform escape %q", forbidden)
		}
	}
}

func TestRecursiveContractReferencesExist(t *testing.T) {
	contract, err := os.ReadFile("RECURSIVE_WATCH_CONTRACT.md")
	if err != nil {
		t.Fatal(err)
	}
	contractTests := regexp.MustCompile("`(Test[A-Za-z0-9_]+(?:/[a-z0-9_]+)?)`").
		FindAllStringSubmatch(string(contract), -1)

	defined := make(map[string]struct{})
	goFiles, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range goFiles {
		file, err := parser.ParseFile(
			token.NewFileSet(),
			path,
			nil,
			parser.SkipObjectResolution,
		)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "Test") {
				continue
			}
			defined[function.Name.Name] = struct{}{}
		}
	}

	contractSource, err := os.ReadFile("recursive_contract_test.go")
	if err != nil {
		t.Fatal(err)
	}
	subtests := regexp.MustCompile(`t\.Run\("([a-z0-9_]+)"`).
		FindAllStringSubmatch(string(contractSource), -1)
	for _, match := range subtests {
		defined["TestRecursiveContract/"+match[1]] = struct{}{}
	}

	for _, match := range contractTests {
		if _, ok := defined[match[1]]; !ok {
			t.Errorf("contract references missing test %q", match[1])
		}
	}
}

var nonRecursiveGoEvidence = map[string]func(*testing.T){
	"TestNonRecursiveWindowsAncestorRenameBlocked": proveNonRecursiveWindowsAncestorRenameBlocked,
}

func validateNonRecursiveEvidenceReference(t *testing.T, exceptionID, evidence string) {
	t.Helper()

	switch {
	case strings.HasPrefix(evidence, "script:"):
		path := strings.TrimPrefix(evidence, "script:")
		if strings.Contains(filepath.ToSlash(path), "testdata/watch-recurse/") {
			t.Errorf("%s: recursive evidence is not valid non-recursive evidence: %q", exceptionID, evidence)
			return
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: evidence %q: %v", exceptionID, evidence, err)
		}
	case strings.HasPrefix(evidence, "go:"):
		name := strings.TrimPrefix(evidence, "go:")
		if _, ok := nonRecursiveGoEvidence[name]; !ok {
			t.Errorf("%s: Go evidence %q is not registered", exceptionID, name)
		}
	default:
		t.Errorf("%s: evidence %q must use script: or go:", exceptionID, evidence)
	}
}

func runNonRecursiveEvidence(t *testing.T, evidence string) {
	t.Helper()

	switch {
	case strings.HasPrefix(evidence, "script:"):
		path := strings.TrimPrefix(evidence, "script:")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if reason := evidenceScriptSkipReason(string(data), runtime.GOOS); reason != "" {
			t.Fatalf("non-recursive evidence %q would not execute on %s: %s", path, runtime.GOOS, reason)
		}
		parseScript(t, string(data))
	case strings.HasPrefix(evidence, "go:"):
		name := strings.TrimPrefix(evidence, "go:")
		test, ok := nonRecursiveGoEvidence[name]
		if !ok {
			t.Fatalf("Go evidence %q is not registered", name)
		}
		test(t)
	default:
		t.Fatalf("unsupported evidence reference %q", evidence)
	}
}

func proveNonRecursiveWindowsAncestorRenameBlocked(t *testing.T) {
	t.Helper()

	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	watcher, err := NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := watcher.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	})
	if err := watcher.Add(child); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(parent, filepath.Join(root, "renamed")); err == nil {
		t.Fatal("renaming an ancestor with an independently watched descendant unexpectedly succeeded")
	}
}

func platformListMatches(platforms []string, goos string) bool {
	for _, platform := range platforms {
		switch platform {
		case goos:
			return true
		case "inotify":
			if goos == "linux" {
				return true
			}
		case "iocp":
			if goos == "windows" {
				return true
			}
		case "kqueue":
			if slices.Contains([]string{"darwin", "dragonfly", "freebsd", "netbsd", "openbsd"}, goos) {
				return true
			}
		case "fen":
			if slices.Contains([]string{"illumos", "solaris"}, goos) {
				return true
			}
		}
	}
	return false
}

func isKnownPlatformSelector(platform string) bool {
	return slices.Contains([]string{
		"darwin",
		"dragonfly",
		"fen",
		"freebsd",
		"illumos",
		"inotify",
		"iocp",
		"kqueue",
		"linux",
		"netbsd",
		"openbsd",
		"solaris",
		"windows",
	}, platform)
}

func evidenceScriptSkipReason(source, goos string) string {
	for _, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if index := strings.IndexByte(line, '#'); index >= 0 {
			line = strings.TrimSpace(line[:index])
		}
		if line == "" {
			continue
		}
		if line == "Output:" {
			return ""
		}

		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		switch fields[0] {
		case "skip":
			if fields[1] == "always" || fields[1] == goos {
				return line
			}
		case "require":
			switch fields[1] {
			case "filter", "op_all", "op_open", "op_read", "op_close_write", "op_close_read":
				if goos != "linux" {
					return line
				}
			default:
				return "environment-dependent " + line
			}
		}
	}
	return ""
}

func sanitizeEvidenceName(evidence string) string {
	replacer := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return replacer.Replace(evidence)
}

func discoverRecursiveExceptionGuards(t *testing.T) map[string]string {
	t.Helper()

	guards := make(map[string]string)
	entries, err := os.ReadDir("testdata/watch-recurse")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.ToSlash(filepath.Join("testdata/watch-recurse", entry.Name()))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanRecursiveScriptGuards(t, guards, path, string(data))
	}

	goFiles, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(goFiles)
	markerPattern := regexp.MustCompile(`recursive-exception:\s+(RPE-[A-Z0-9-]+)`)
	for _, path := range goFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			match := markerPattern.FindStringSubmatch(line)
			if len(match) == 0 {
				continue
			}
			guard := fmt.Sprintf("%s|source-marker|%s", filepath.ToSlash(path), match[1])
			addDiscoveredGuard(t, guards, guard, fmt.Sprintf("%s:%d", path, lineNo+1))
		}
	}
	return guards
}

func scanRecursiveScriptGuards(t *testing.T, guards map[string]string, path, source string) {
	t.Helper()

	output := false
	outputGroup := regexp.MustCompile(`^([a-z][a-z0-9]*(?:\s*,\s*[a-z][a-z0-9]*)*):$`)
	for lineNo, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if index := strings.IndexByte(line, '#'); index >= 0 {
			line = strings.TrimSpace(line[:index])
		}
		if line == "" {
			continue
		}
		if line == "Output:" {
			output = true
			continue
		}

		if !output {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "skip" {
				guard := fmt.Sprintf("%s|skip|%s", path, fields[1])
				addDiscoveredGuard(t, guards, guard, fmt.Sprintf("%s:%d", path, lineNo+1))
			}
			if len(fields) == 2 && fields[0] == "require" &&
				fields[1] != "recurse" {
				guard := fmt.Sprintf("%s|require|%s", path, fields[1])
				addDiscoveredGuard(t, guards, guard, fmt.Sprintf("%s:%d", path, lineNo+1))
			}
			continue
		}

		match := outputGroup.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		selectors := strings.Split(match[1], ",")
		for i := range selectors {
			selectors[i] = strings.TrimSpace(selectors[i])
		}
		sort.Strings(selectors)
		guard := fmt.Sprintf("%s|output|%s", path, strings.Join(selectors, ","))
		addDiscoveredGuard(t, guards, guard, fmt.Sprintf("%s:%d", path, lineNo+1))
	}
}

func addDiscoveredGuard(t *testing.T, guards map[string]string, guard, location string) {
	t.Helper()
	if previous, exists := guards[guard]; exists {
		t.Fatalf("duplicate discovered guard %q at %s and %s", guard, previous, location)
	}
	guards[guard] = location
}
