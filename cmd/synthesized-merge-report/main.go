package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const manifestPath = "CONTRACTS/tekroo.kernel.contracts/0.2.0/manifest.json"

type mergePlan struct {
	SchemaVersion         string   `json:"schemaVersion"`
	RepositoryURL         string   `json:"repositoryUrl"`
	BaseRef               string   `json:"baseRef"`
	BaseCommit            string   `json:"baseCommit"`
	OrderedHeads          []head   `json:"orderedHeads"`
	Strategy              string   `json:"strategy"`
	GitVersion            string   `json:"gitVersion"`
	ConflictPolicy        string   `json:"conflictPolicy"`
	ContractIdentity      string   `json:"contractIdentity"`
	ManifestSHA256        string   `json:"manifestSha256"`
	RequiredProfiles      []string `json:"requiredProfiles"`
	ExpectedCandidateTree string   `json:"expectedCandidateTree"`
}

type head struct {
	Commit string `json:"commit"`
	Role   string `json:"role"`
}

type gateReport struct {
	SchemaVersion       string          `json:"schemaVersion"`
	ReportType          string          `json:"reportType"`
	Profile             string          `json:"profile"`
	Status              string          `json:"status"`
	RepositoryURL       string          `json:"repositoryUrl"`
	PlanPath            string          `json:"planPath"`
	PlanSHA256          string          `json:"planSha256"`
	BaseRef             string          `json:"baseRef"`
	BaseCommit          string          `json:"baseCommit"`
	OrderedHeads        []head          `json:"orderedHeads"`
	Strategy            string          `json:"strategy"`
	GitVersion          string          `json:"gitVersion"`
	ConflictPolicy      string          `json:"conflictPolicy"`
	CandidateCommit     string          `json:"candidateCommit"`
	CandidateTree       string          `json:"candidateTree"`
	ContractIdentity    string          `json:"contractIdentity"`
	ManifestSHA256      string          `json:"manifestSha256"`
	Environment         environment     `json:"environment"`
	RequiredProfiles    []profileResult `json:"requiredProfiles"`
	CandidateCleanAfter bool            `json:"candidateCleanAfter"`
	BaseRefStableAfter  bool            `json:"baseRefStableAfter"`
	Artifacts           []artifact      `json:"artifacts"`
	EvidenceFloor       []string        `json:"evidenceFloor"`
	UnresolvedGaps      []string        `json:"unresolvedGaps"`
	DigestMethod        string          `json:"digestMethod"`
	ReportSHA256        string          `json:"reportSha256"`
}

type environment struct {
	GoVersion string `json:"goVersion"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	Node      string `json:"nodeVersion"`
	Mongo     string `json:"mongoDbVersionLine"`
	Locale    string `json:"locale"`
	TimeZone  string `json:"timeZone"`
}

type profileResult struct {
	Profile    string `json:"profile"`
	Status     string `json:"status"`
	ReportPath string `json:"reportPath"`
	SHA256     string `json:"sha256"`
	Verifier   string `json:"verifier"`
}

type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "-verify" {
		if len(os.Args) != 3 {
			fatal(errors.New("usage: synthesized-merge-report -verify report"))
		}
		if err := verify(os.Args[2]); err != nil {
			fatal(err)
		}
		return
	}
	flags := flag.NewFlagSet("synthesized-merge-report", flag.ContinueOnError)
	planPath := flags.String("plan", "", "frozen merge plan path")
	output := flags.String("output", "OUTPUT/phase-2/step-4-synthesized-merge-gate.json", "gate report output")
	artifactDir := flags.String("artifacts-dir", "OUTPUT/phase-2/step-4-synthesized-merge", "artifact directory")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fatal(err)
	}
	if *planPath == "" || flags.NArg() != 0 {
		fatal(errors.New("usage: synthesized-merge-report -plan path [-output path] [-artifacts-dir path]"))
	}
	if err := run(*planPath, *output, *artifactDir); err != nil {
		fatal(err)
	}
}

func run(planPath, output, artifactDir string) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	if !safeRelative(output) || !safeRelative(artifactDir) {
		return errors.New("output and artifact paths must be safe repository-relative paths")
	}
	if dirty, err := gitOutput(root, "status", "--porcelain"); err != nil || dirty != "" {
		if err != nil {
			return err
		}
		return errors.New("source repository input is dirty")
	}
	finalArtifactDir := filepath.Join(root, artifactDir)
	finalOutput := filepath.Join(root, output)
	if _, err := os.Stat(finalArtifactDir); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("artifact directory already exists")
	}
	if _, err := os.Stat(finalOutput); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("gate report already exists")
	}
	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var plan mergePlan
	if err := json.Unmarshal(planBytes, &plan); err != nil {
		return err
	}
	if err := validatePlan(root, plan); err != nil {
		return err
	}
	planDigest := digestBytes(planBytes)
	buildRoot := filepath.Join(root, "build")
	if err := os.MkdirAll(buildRoot, 0o755); err != nil {
		return err
	}
	temporaryRoot, err := os.MkdirTemp(buildRoot, "synthesized-merge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporaryRoot)
	publicationRoot := filepath.Join(temporaryRoot, "publication")
	workingArtifactDir := filepath.Join(publicationRoot, artifactDir)
	if err := os.MkdirAll(workingArtifactDir, 0o755); err != nil {
		return err
	}
	candidate := filepath.Join(temporaryRoot, "candidate")
	if _, err := commandOutput("", "git", "clone", "--no-hardlinks", "--no-checkout", root, candidate); err != nil {
		return err
	}
	if _, err := gitOutput(candidate, "checkout", "--detach", plan.BaseCommit); err != nil {
		return err
	}
	for _, item := range plan.OrderedHeads {
		if _, err := gitOutput(candidate, "merge", "--ff-only", item.Commit); err != nil {
			return fmt.Errorf("merge head %s: %w", item.Commit, err)
		}
	}
	candidateCommit, err := gitOutput(candidate, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	candidateTree, err := gitOutput(candidate, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	if candidateCommit != plan.OrderedHeads[len(plan.OrderedHeads)-1].Commit || candidateTree != plan.ExpectedCandidateTree {
		return errors.New("synthesized candidate identity differs from frozen plan")
	}
	planArtifact := filepath.Join(workingArtifactDir, "merge-plan.json")
	if err := os.WriteFile(planArtifact, planBytes, 0o644); err != nil {
		return err
	}
	structureArtifact := filepath.Join(workingArtifactDir, "contract-structure.json")
	if _, err := commandOutput(candidate, "node", filepath.Join("CONTRACTS", "tekroo.kernel.contracts", "0.2.0", "runner", "validate-package.mjs"), structureArtifact); err != nil {
		return err
	}
	if err := requireJSONStatus(structureArtifact, "PASS"); err != nil {
		return err
	}
	coreClone := filepath.Join(temporaryRoot, "core")
	if _, err := commandOutput("", "git", "clone", "--no-hardlinks", "--no-checkout", candidate, coreClone); err != nil {
		return err
	}
	if _, err := gitOutput(coreClone, "checkout", "--detach", candidateCommit); err != nil {
		return err
	}
	coreArtifactInClone := filepath.Join(coreClone, "build", "reports", "core-hermetic.json")
	if _, err := commandOutput(coreClone, "go", "run", "./cmd/core-hermetic-report", "-output", coreArtifactInClone); err != nil {
		return err
	}
	if _, err := commandOutput(coreClone, "go", "run", "./cmd/core-hermetic-report", "-verify", coreArtifactInClone); err != nil {
		return err
	}
	coreArtifact := filepath.Join(workingArtifactDir, "core-hermetic.json")
	if err := copyFile(coreArtifactInClone, coreArtifact); err != nil {
		return err
	}
	if err := copyIfPresent(filepath.Join(coreClone, "build", "reports", "contract-structure.core-run.json"), filepath.Join(workingArtifactDir, "core-contract-structure.json")); err != nil {
		return err
	}
	if err := copyIfPresent(filepath.Join(coreClone, "build", "reports", "reference-corpus.core-run.json"), filepath.Join(workingArtifactDir, "core-reference-corpus.json")); err != nil {
		return err
	}
	if err := copyIfPresent(filepath.Join(coreClone, "OUTPUT", "phase-2", "step-2-core-mutations.json"), filepath.Join(workingArtifactDir, "core-mutations.json")); err != nil {
		return err
	}
	if err := copyDir(filepath.Join(coreClone, "OUTPUT", "phase-2", "step-2-core-mutations"), filepath.Join(workingArtifactDir, "core-mutations")); err != nil {
		return err
	}
	mongoArtifactRelative := filepath.ToSlash(filepath.Join(artifactDir, "mongo-integration.json"))
	mongoRawRelative := filepath.ToSlash(filepath.Join(artifactDir, "mongo-integration.raw.jsonl"))
	mongoArtifact := filepath.Join(publicationRoot, filepath.FromSlash(mongoArtifactRelative))
	mongoRaw := filepath.Join(publicationRoot, filepath.FromSlash(mongoRawRelative))
	if _, err := commandOutput(candidate, "go", "run", "./cmd/mongo-integration-report", "-output", mongoArtifact, "-raw-output", mongoRaw, "-raw-artifact-path", mongoRawRelative); err != nil {
		return err
	}
	if _, err := commandOutput(candidate, "go", "run", "./cmd/mongo-integration-report", "-verify", mongoArtifact, publicationRoot); err != nil {
		return err
	}
	candidateStatus, err := gitOutput(candidate, "status", "--porcelain")
	if err != nil {
		return err
	}
	baseAfter, err := gitOutput(root, "rev-parse", plan.BaseRef)
	if err != nil {
		return err
	}
	if candidateStatus != "" || baseAfter != plan.BaseCommit {
		return errors.New("candidate became dirty or frozen base reference moved")
	}
	if err := requireJSONStatus(coreArtifact, "PASS"); err != nil {
		return err
	}
	if err := requireJSONStatus(mongoArtifact, "PASS"); err != nil {
		return err
	}
	nodeVersion, err := firstLine("", "node", "--version")
	if err != nil {
		return err
	}
	mongoVersion, err := firstLine("", "mongod", "--version")
	if err != nil {
		return err
	}
	profiles := []profileResult{
		profile("contract-structure", structureArtifact, publicationRoot, "contract package validator exited successfully and report status is PASS"),
		profile("core-hermetic", coreArtifact, publicationRoot, "core-hermetic report verifier exited successfully in an exact candidate clone"),
		profile("mongo-integration", mongoArtifact, publicationRoot, "mongo-integration report verifier exited successfully against the exact candidate tree"),
	}
	if err := os.MkdirAll(filepath.Dir(finalArtifactDir), 0o755); err != nil {
		return err
	}
	if err := os.Rename(workingArtifactDir, finalArtifactDir); err != nil {
		return err
	}
	artifacts, err := collectArtifacts(root, artifactDir)
	if err != nil {
		return err
	}
	value := gateReport{
		SchemaVersion: "1.0.0", ReportType: "TEKROO_PHASE_2_SYNTHESIZED_MERGE_GATE", Profile: "synthesized-merge", Status: "PASS",
		RepositoryURL: plan.RepositoryURL, PlanPath: filepath.ToSlash(filepath.Join(artifactDir, "merge-plan.json")), PlanSHA256: planDigest,
		BaseRef: plan.BaseRef, BaseCommit: plan.BaseCommit, OrderedHeads: plan.OrderedHeads, Strategy: plan.Strategy,
		GitVersion: plan.GitVersion, ConflictPolicy: plan.ConflictPolicy, CandidateCommit: candidateCommit, CandidateTree: candidateTree,
		ContractIdentity: plan.ContractIdentity, ManifestSHA256: plan.ManifestSHA256,
		Environment:      environment{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Node: nodeVersion, Mongo: mongoVersion, Locale: "C", TimeZone: "UTC"},
		RequiredProfiles: profiles, CandidateCleanAfter: true, BaseRefStableAfter: true, Artifacts: artifacts,
		EvidenceFloor: []string{
			"OBSERVED: an isolated clone fast-forwarded each exact ordered head from the frozen base and produced the planned candidate tree.",
			"OBSERVED: contract-structure, core-hermetic, and mongo-integration reports each returned PASS and their independent verifiers completed where defined.",
			"COMPUTED: candidate and artifact identities are SHA-256/Git object digests recorded in this report.",
			"INFERRED only for the exact candidate tree: the deterministic profiles support merge qualification; they do not qualify provider E2E, migration, production topology, performance, deployment, or a different tree.",
		},
		UnresolvedGaps: []string{}, DigestMethod: "SHA-256 over compact JSON with reportSha256 set to the empty string",
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	value.ReportSHA256 = digestBytes(encoded)
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalOutput), 0o755); err != nil {
		return err
	}
	return os.WriteFile(finalOutput, append(pretty, '\n'), 0o644)
}

func validatePlan(root string, plan mergePlan) error {
	if plan.SchemaVersion != "1.0.0" || plan.RepositoryURL == "" || plan.BaseRef == "" || len(plan.OrderedHeads) == 0 || plan.Strategy != "FF_ONLY_ORDERED" || plan.ConflictPolicy != "FAIL_NO_IMPROVISATION" || plan.ContractIdentity != "tekroo.kernel.contracts/0.2.0" || len(plan.ExpectedCandidateTree) != 40 {
		return errors.New("invalid or incomplete merge plan")
	}
	if !equalStrings(plan.RequiredProfiles, []string{"contract-structure", "core-hermetic", "mongo-integration"}) {
		return errors.New("required profile list differs from synthesized-merge policy")
	}
	repositoryURL, err := gitOutput(root, "config", "--get", "remote.origin.url")
	if err != nil || repositoryURL != plan.RepositoryURL {
		return errors.New("repository identity mismatch")
	}
	base, err := gitOutput(root, "rev-parse", plan.BaseRef)
	if err != nil || base != plan.BaseCommit {
		return errors.New("base reference mismatch")
	}
	gitVersion, err := firstLine(root, "git", "--version")
	if err != nil || gitVersion != plan.GitVersion {
		return errors.New("git version mismatch")
	}
	manifestDigest, err := fileDigest(filepath.Join(root, manifestPath))
	if err != nil || manifestDigest != plan.ManifestSHA256 {
		return errors.New("contract manifest mismatch")
	}
	headNow, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil || headNow != plan.OrderedHeads[len(plan.OrderedHeads)-1].Commit {
		return errors.New("authoritative HEAD differs from final planned head")
	}
	prior := plan.BaseCommit
	seen := make(map[string]bool)
	for _, item := range plan.OrderedHeads {
		if len(item.Commit) != 40 || item.Role == "" || seen[item.Commit] {
			return errors.New("invalid or duplicate ordered head")
		}
		seen[item.Commit] = true
		if _, err := gitOutput(root, "cat-file", "-e", item.Commit+"^{commit}"); err != nil {
			return err
		}
		if err := commandSuccess(root, "git", "merge-base", "--is-ancestor", prior, item.Commit); err != nil {
			return errors.New("ordered heads are not a fast-forward chain")
		}
		prior = item.Commit
	}
	tree, err := gitOutput(root, "rev-parse", prior+"^{tree}")
	if err != nil || tree != plan.ExpectedCandidateTree {
		return errors.New("planned final head tree mismatch")
	}
	return nil
}

func verify(path string) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var value gateReport
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	reported := value.ReportSHA256
	value.ReportSHA256 = ""
	canonical, err := json.Marshal(value)
	if err != nil || digestBytes(canonical) != reported {
		return errors.New("report self-digest mismatch")
	}
	if value.Status != "PASS" || !value.CandidateCleanAfter || !value.BaseRefStableAfter {
		return errors.New("report does not contain a passing gate")
	}
	planDigest, err := fileDigest(filepath.Join(root, filepath.FromSlash(value.PlanPath)))
	if err != nil || planDigest != value.PlanSHA256 {
		return errors.New("merge plan digest mismatch")
	}
	manifestDigest, err := fileDigest(filepath.Join(root, manifestPath))
	if err != nil || manifestDigest != value.ManifestSHA256 {
		return errors.New("manifest digest mismatch")
	}
	base, err := gitOutput(root, "rev-parse", value.BaseRef)
	if err != nil || base != value.BaseCommit {
		return errors.New("base reference moved")
	}
	tree, err := gitOutput(root, "rev-parse", value.CandidateCommit+"^{tree}")
	if err != nil || tree != value.CandidateTree {
		return errors.New("candidate tree mismatch")
	}
	for _, item := range value.Artifacts {
		path := filepath.Join(root, filepath.FromSlash(item.Path))
		info, err := os.Stat(path)
		if err != nil || info.Size() != item.Size {
			return fmt.Errorf("artifact size mismatch: %s", item.Path)
		}
		digest, err := fileDigest(path)
		if err != nil || digest != item.SHA256 {
			return fmt.Errorf("artifact digest mismatch: %s", item.Path)
		}
	}
	return nil
}

func profile(name, path, root, verifier string) profileResult {
	digest, _ := fileDigest(path)
	relative, _ := filepath.Rel(root, path)
	return profileResult{Profile: name, Status: "PASS", ReportPath: filepath.ToSlash(relative), SHA256: digest, Verifier: verifier}
}

func requireJSONStatus(path, wanted string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var value struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Status != wanted {
		return fmt.Errorf("report %s status = %s, want %s", path, value.Status, wanted)
	}
	return nil
}

func collectArtifacts(root, directory string) ([]artifact, error) {
	var result []artifact
	abs := filepath.Join(root, directory)
	err := filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		digest, err := fileDigest(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result = append(result, artifact{Path: filepath.ToSlash(relative), SHA256: digest, Size: info.Size()})
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, err
}

func repositoryRoot() (string, error) {
	root, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	working, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if filepath.Clean(root) != filepath.Clean(working) {
		return "", errors.New("run from repository root")
	}
	return root, nil
}

func gitOutput(directory string, arguments ...string) (string, error) {
	output, err := commandOutput(directory, "git", arguments...)
	return strings.TrimSpace(string(output)), err
}

func firstLine(directory, name string, arguments ...string) (string, error) {
	output, err := commandOutput(directory, name, arguments...)
	if err != nil {
		return "", err
	}
	line, _, _ := bytes.Cut(output, []byte{'\n'})
	return strings.TrimSpace(string(line)), nil
}

func commandSuccess(directory, name string, arguments ...string) error {
	_, err := commandOutput(directory, name, arguments...)
	return err
}

func commandOutput(directory, name string, arguments ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return output, fmt.Errorf("%s %s: %w", name, strings.Join(arguments, " "), ctx.Err())
	}
	if err != nil {
		return output, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(arguments, " "), err, output)
	}
	return output, nil
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func copyIfPresent(source, target string) error {
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return copyFile(source, target)
}

func copyDir(source, target string) error {
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		return copyFile(path, destination)
	})
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func safeRelative(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
