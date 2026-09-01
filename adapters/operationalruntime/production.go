package operationalruntime

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tekroo-ai/teams/adapters/executionruntime"
	"github.com/tekroo-ai/teams/adapters/federationhttp"
	"github.com/tekroo-ai/teams/adapters/gitprovider"
	"github.com/tekroo-ai/teams/adapters/mongo"
	"github.com/tekroo-ai/teams/adapters/openhands"
	"github.com/tekroo-ai/teams/adapters/protocol"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/contract"
	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

const (
	ContractPackagePath = "CONTRACTS/tekroo.kernel.contracts/0.10.0"
	ManifestSHA256      = kernel.Digest("2752b876d5a71bb1367a088b9f8cc0ad5df6343b0833906c49aae5a404b8db98")
	maximumConfigBytes  = 1 << 20
)

var ErrInvalidProductionConfiguration = errors.New("invalid tekrood configuration")

// ProductionConfig is the non-secret, file-backed configuration for tekrood.
// Credentials and policy/provenance documents are referenced by path so they
// never need to be embedded in the service configuration or process listing.
type ProductionConfig struct {
	ContractRoot            string                    `json:"contract_root"`
	Mongo                   ProductionMongoConfig     `json:"mongo"`
	OpenHands               ProductionOpenHandsConfig `json:"openhands"`
	Operator                ProductionOperatorConfig  `json:"operator"`
	TeamsDatabaseIdentity   string                    `json:"teams_database_identity"`
	SMADatabaseIdentity     string                    `json:"sma_database_identity"`
	DeploymentIdentity      kernel.Digest             `json:"deployment_identity_digest"`
	AuthorizationPolicyFile string                    `json:"authorization_policy_file"`
	ProvenanceFile          string                    `json:"provenance_file"`
	EvidenceRoot            string                    `json:"evidence_root"`
	ServiceAuthority        kernel.PrincipalRef       `json:"service_authority"`
	ExpiryAuthority         kernel.PrincipalRef       `json:"expiry_authority"`
	Workspaces              []ProductionWorkspace     `json:"workspaces"`
	Profiles                []ProductionProfile       `json:"profiles"`
	Execution               ProductionExecution       `json:"execution"`
	Evidence                ProductionEvidence        `json:"evidence"`
	Worker                  ProductionWorker          `json:"worker"`
	Projection              ProductionProjection      `json:"projection"`
	Organization            ProductionOrganization    `json:"organization"`
	Planning                ProductionPlanning        `json:"planning"`
	Git                     *ProductionGitConfig      `json:"git,omitempty"`
	Federation              *ProductionFederation     `json:"federation,omitempty"`
}

type ProductionFederation struct {
	Address           string                              `json:"address"`
	MaximumBodyBytes  int64                               `json:"maximum_body_bytes"`
	RequestTimeout    string                              `json:"request_timeout"`
	AllowedFutureSkew string                              `json:"allowed_future_skew"`
	EnvelopeTTL       string                              `json:"envelope_ttl"`
	PrivateKeyFile    string                              `json:"private_key_file"`
	SigningIdentity   organization.FederationTrustGrant   `json:"signing_identity"`
	Aliases           []organization.AliasBinding         `json:"aliases"`
	Routes            []organization.FederationRoute      `json:"routes"`
	TrustGrants       []organization.FederationTrustGrant `json:"trust_grants"`
}

type ProductionGitConfig struct {
	Binary           string `json:"binary"`
	AllowedRoot      string `json:"allowed_root"`
	OperationTimeout string `json:"operation_timeout"`
}

type ProductionMongoConfig struct {
	URIFile                string `json:"uri_file"`
	Database               string `json:"database"`
	BacklogLimit           int64  `json:"backlog_limit"`
	DeliveryPolicyRevision uint64 `json:"delivery_policy_revision"`
}

type ProductionOpenHandsConfig struct {
	BaseURL              string `json:"base_url"`
	SessionAPIKeyFile    string `json:"session_api_key_file"`
	RequestTimeout       string `json:"request_timeout"`
	PollInterval         string `json:"poll_interval"`
	MaximumPages         uint32 `json:"maximum_pages"`
	MaximumEvidenceBytes int    `json:"maximum_evidence_bytes"`
}

type ProductionOperatorConfig struct {
	Address          string                      `json:"address"`
	BearerTokenFile  string                      `json:"bearer_token_file"`
	Principal        kernel.PrincipalRef         `json:"principal"`
	HumanCredentials []ProductionHumanCredential `json:"human_credentials,omitempty"`
	OperationTimeout string                      `json:"operation_timeout"`
	MaximumBodyBytes int64                       `json:"maximum_body_bytes"`
}

type ProductionHumanCredential struct {
	Principal       kernel.PrincipalRef `json:"principal"`
	BearerTokenFile string              `json:"bearer_token_file"`
}

type HumanTransportCredential struct {
	Principal kernel.PrincipalRef
	Token     string
}

type ProductionWorkspace struct {
	WorkspaceID      string   `json:"workspace_id"`
	WorktreeID       string   `json:"worktree_id"`
	WorkingDirectory string   `json:"working_directory"`
	Branch           string   `json:"branch"`
	BaselineSHA      string   `json:"baseline_sha"`
	WritablePaths    []string `json:"writable_paths"`
}

type ProductionProfile struct {
	ModelProfileDigest    kernel.Digest                         `json:"model_profile_digest"`
	RuntimeIdentityDigest kernel.Digest                         `json:"runtime_identity_digest"`
	ToolPolicyDigest      kernel.Digest                         `json:"tool_policy_digest"`
	EffectPolicyDigest    kernel.Digest                         `json:"effect_policy_digest"`
	MaximumIterations     uint32                                `json:"maximum_iterations"`
	Qualification         kernel.AssignmentQualificationReceipt `json:"qualification"`
}

type ProductionPlanning struct {
	PolicyRevision             uint64        `json:"policy_revision"`
	ClassificationPolicyDigest kernel.Digest `json:"classification_policy_digest"`
	PromotionPolicyDigest      kernel.Digest `json:"promotion_policy_digest"`
	VerificationTopologyDigest kernel.Digest `json:"verification_topology_digest"`
	SelectionPolicyDigest      kernel.Digest `json:"selection_policy_digest"`
	BudgetPolicyDigest         kernel.Digest `json:"budget_policy_digest"`
	RequiredGateIDs            []string      `json:"required_gate_ids"`
	Deadline                   string        `json:"deadline"`
}

type ProductionExecution struct {
	ConsumerID        string `json:"consumer_id"`
	OperationTimeout  string `json:"operation_timeout"`
	MaximumBriefBytes int    `json:"maximum_brief_bytes"`
	PolicyRevision    uint64 `json:"policy_revision"`
}

type ProductionEvidence struct {
	PolicyRevision   uint64 `json:"policy_revision"`
	ProducingVersion string `json:"producing_version"`
	RetentionPolicy  string `json:"retention_policy"`
}

type ProductionWorker struct {
	LeaseDuration                string `json:"lease_duration"`
	ReconciliationInterval       string `json:"reconciliation_interval"`
	MaximumReconciliations       uint32 `json:"maximum_reconciliations"`
	MaximumConcurrentInvocations uint32 `json:"maximum_concurrent_invocations"`
	LeaseOperationTimeout        string `json:"lease_operation_timeout"`
}

type ProductionProjection struct {
	Interval         string `json:"interval"`
	OperationTimeout string `json:"operation_timeout"`
}

type ProductionOrganization struct {
	ManifestFile            string                 `json:"manifest_file"`
	ManifestDigest          kernel.Digest          `json:"manifest_digest"`
	LibraryManifests        []ProductionTeamSource `json:"library_manifests,omitempty"`
	Publishers              []ProductionPublisher  `json:"trusted_publishers"`
	ReconciliationInterval  string                 `json:"reconciliation_interval"`
	MaximumRestarts         uint32                 `json:"maximum_restarts"`
	MaximumDeliveryAttempts uint32                 `json:"maximum_delivery_attempts"`
}

type ProductionTeamSource struct {
	ManifestFile   string        `json:"manifest_file"`
	ManifestDigest kernel.Digest `json:"manifest_digest"`
}

type ProductionPublisher struct {
	KeyID         string `json:"key_id"`
	PublicKeyFile string `json:"public_key_file"`
}

type resolvedProductionConfig struct {
	ProductionConfig
	mongoURI              string
	sessionAPIKey         string
	operatorBearerToken   string
	humanCredentials      []HumanTransportCredential
	authorizationPolicy   kernel.AuthorizationPolicy
	provenance            kernel.ProvenanceBasis
	requestTimeout        time.Duration
	pollInterval          time.Duration
	operationTimeout      time.Duration
	leaseDuration         time.Duration
	reconciliation        time.Duration
	leaseOperationTimeout time.Duration
	projectionInterval    time.Duration
	projectionTimeout     time.Duration
	operatorTimeout       time.Duration
	team                  organization.LoadedTeam
	libraryTeams          []organization.LoadedTeam
	trustedRolePublishers map[string]ed25519.PublicKey
	federationRegistry    *organization.StaticFederationRegistry
	federationPrivateKey  ed25519.PrivateKey
	federationTimeout     time.Duration
	federationFutureSkew  time.Duration
	federationTTL         time.Duration
	roleReconciliation    time.Duration
	planningDeadline      time.Duration
	gitOperationTimeout   time.Duration
	planning              ProductionPlanning
	profilesByModel       map[kernel.Digest]ProductionProfile
	workspacesByID        map[string]ProductionWorkspace
	serviceAuthority      kernel.PrincipalRef
	policyAuthority       kernel.PrincipalRef
}

// LoadProductionConfig strictly decodes and validates a tekrood configuration.
// Relative file paths are resolved against the configuration file directory.
func LoadProductionConfig(path string) (ProductionConfig, error) {
	if !filepath.IsAbs(path) {
		return ProductionConfig{}, invalidConfig("configuration path must be absolute")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ProductionConfig{}, fmt.Errorf("%w: read configuration: %v", ErrInvalidProductionConfiguration, err)
	}
	if len(raw) == 0 || len(raw) > maximumConfigBytes {
		return ProductionConfig{}, invalidConfig("configuration file size is invalid")
	}
	var config ProductionConfig
	if err := decodeStrict(raw, &config); err != nil {
		return ProductionConfig{}, fmt.Errorf("%w: decode configuration: %v", ErrInvalidProductionConfiguration, err)
	}
	base := filepath.Dir(path)
	config.ContractRoot = absoluteFrom(base, config.ContractRoot)
	config.Mongo.URIFile = absoluteFrom(base, config.Mongo.URIFile)
	config.OpenHands.SessionAPIKeyFile = absoluteFrom(base, config.OpenHands.SessionAPIKeyFile)
	config.Operator.BearerTokenFile = absoluteFrom(base, config.Operator.BearerTokenFile)
	for index := range config.Operator.HumanCredentials {
		config.Operator.HumanCredentials[index].BearerTokenFile = absoluteFrom(base, config.Operator.HumanCredentials[index].BearerTokenFile)
	}
	config.AuthorizationPolicyFile = absoluteFrom(base, config.AuthorizationPolicyFile)
	config.ProvenanceFile = absoluteFrom(base, config.ProvenanceFile)
	config.EvidenceRoot = absoluteFrom(base, config.EvidenceRoot)
	config.Organization.ManifestFile = absoluteFrom(base, config.Organization.ManifestFile)
	for index := range config.Organization.LibraryManifests {
		config.Organization.LibraryManifests[index].ManifestFile = absoluteFrom(base, config.Organization.LibraryManifests[index].ManifestFile)
	}
	for index := range config.Organization.Publishers {
		config.Organization.Publishers[index].PublicKeyFile = absoluteFrom(base, config.Organization.Publishers[index].PublicKeyFile)
	}
	for index := range config.Workspaces {
		config.Workspaces[index].WorkingDirectory = absoluteFrom(base, config.Workspaces[index].WorkingDirectory)
	}
	if config.Git != nil {
		config.Git.AllowedRoot = absoluteFrom(base, config.Git.AllowedRoot)
	}
	if config.Federation != nil {
		config.Federation.PrivateKeyFile = absoluteFrom(base, config.Federation.PrivateKeyFile)
	}
	if _, err := resolveProductionConfig(config); err != nil {
		return ProductionConfig{}, err
	}
	return config, nil
}

func resolveProductionConfig(config ProductionConfig) (resolvedProductionConfig, error) {
	if config.ContractRoot == "" || config.Mongo.Database == "" || config.Mongo.Database != config.TeamsDatabaseIdentity || config.SMADatabaseIdentity == "" || config.SMADatabaseIdentity == config.TeamsDatabaseIdentity || !config.DeploymentIdentity.Valid() || config.Mongo.BacklogLimit <= 0 || config.Mongo.DeliveryPolicyRevision == 0 || config.EvidenceRoot == "" || !filepath.IsAbs(config.EvidenceRoot) || len(config.Workspaces) == 0 || len(config.Profiles) == 0 {
		return resolvedProductionConfig{}, invalidConfig("required identity, storage, workspace, or profile binding is missing")
	}
	if info, err := os.Stat(filepath.Join(config.ContractRoot, ContractPackagePath, "manifest.json")); err != nil || !info.Mode().IsRegular() {
		return resolvedProductionConfig{}, invalidConfig("contract root does not contain contract 0.10.0")
	}
	if !loopbackHTTPURL(config.OpenHands.BaseURL) {
		return resolvedProductionConfig{}, invalidConfig("OpenHands base URL must be an explicit loopback HTTP endpoint with no path")
	}
	if !loopbackAddress(config.Operator.Address) || !config.Operator.Principal.Valid() || config.Operator.Principal.Kind != kernel.PrincipalHuman || config.Operator.MaximumBodyBytes <= 0 || config.Operator.MaximumBodyBytes > 1<<20 {
		return resolvedProductionConfig{}, invalidConfig("operator address or body limit is invalid")
	}
	mongoURI, err := readSecret(config.Mongo.URIFile)
	if err != nil || !loopbackMongoURI(mongoURI) {
		return resolvedProductionConfig{}, invalidConfig("Mongo URI file must contain one loopback MongoDB endpoint")
	}
	sessionKey, err := readSecret(config.OpenHands.SessionAPIKeyFile)
	if err != nil || sessionKey == "" {
		return resolvedProductionConfig{}, invalidConfig("OpenHands session API key file is missing or empty")
	}
	operatorToken, err := readSecret(config.Operator.BearerTokenFile)
	if err != nil || len(operatorToken) < 32 {
		return resolvedProductionConfig{}, invalidConfig("operator bearer token file must contain at least 32 characters")
	}
	humanCredentials := make([]HumanTransportCredential, len(config.Operator.HumanCredentials))
	seenHumans := make(map[string]struct{}, len(humanCredentials))
	seenTokens := map[kernel.Digest]struct{}{digestBytes([]byte(operatorToken)): {}}
	for index, credential := range config.Operator.HumanCredentials {
		token, tokenErr := readSecret(credential.BearerTokenFile)
		tokenDigest := digestBytes([]byte(token))
		if tokenErr != nil || len(token) < 32 || credential.Principal.Kind != kernel.PrincipalHuman || !credential.Principal.Valid() || credential.Principal == config.Operator.Principal {
			return resolvedProductionConfig{}, invalidConfig("human transport credential is invalid")
		}
		if _, duplicate := seenHumans[credential.Principal.ID]; duplicate {
			return resolvedProductionConfig{}, invalidConfig("human transport principal is duplicated")
		}
		if _, duplicate := seenTokens[tokenDigest]; duplicate {
			return resolvedProductionConfig{}, invalidConfig("human transport credential is duplicated")
		}
		seenHumans[credential.Principal.ID] = struct{}{}
		seenTokens[tokenDigest] = struct{}{}
		humanCredentials[index] = HumanTransportCredential{Principal: credential.Principal, Token: token}
	}
	requestTimeout, err := positiveDuration("openhands.request_timeout", config.OpenHands.RequestTimeout)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	pollInterval, err := positiveDuration("openhands.poll_interval", config.OpenHands.PollInterval)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	operationTimeout, err := positiveDuration("execution.operation_timeout", config.Execution.OperationTimeout)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	leaseDuration, err := positiveDuration("worker.lease_duration", config.Worker.LeaseDuration)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	reconciliation, err := positiveDuration("worker.reconciliation_interval", config.Worker.ReconciliationInterval)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	leaseOperationTimeout, err := positiveDuration("worker.lease_operation_timeout", config.Worker.LeaseOperationTimeout)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	projectionInterval, err := positiveDuration("projection.interval", config.Projection.Interval)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	projectionTimeout, err := positiveDuration("projection.operation_timeout", config.Projection.OperationTimeout)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	operatorTimeout, err := positiveDuration("operator.operation_timeout", config.Operator.OperationTimeout)
	if err != nil {
		return resolvedProductionConfig{}, err
	}
	roleReconciliation, err := positiveDuration("organization.reconciliation_interval", config.Organization.ReconciliationInterval)
	if err != nil || config.Organization.MaximumRestarts == 0 || config.Organization.MaximumDeliveryAttempts == 0 {
		return resolvedProductionConfig{}, invalidConfig("organization recovery policy is invalid")
	}
	planningDeadline, err := positiveDuration("planning.deadline", config.Planning.Deadline)
	if err != nil || config.Planning.PolicyRevision == 0 || !config.Planning.ClassificationPolicyDigest.Valid() || !config.Planning.PromotionPolicyDigest.Valid() || !config.Planning.VerificationTopologyDigest.Valid() || !config.Planning.SelectionPolicyDigest.Valid() || !config.Planning.BudgetPolicyDigest.Valid() || len(config.Planning.RequiredGateIDs) == 0 || len(config.Planning.RequiredGateIDs) > 32 {
		return resolvedProductionConfig{}, invalidConfig("planning policy is invalid")
	}
	gitOperationTimeout := time.Duration(0)
	if config.Git != nil {
		gitOperationTimeout, err = positiveDuration("git.operation_timeout", config.Git.OperationTimeout)
		if err != nil || !filepath.IsAbs(config.Git.AllowedRoot) {
			return resolvedProductionConfig{}, invalidConfig("Git release-provider configuration is invalid")
		}
		if _, providerErr := gitprovider.New(gitprovider.Config{GitBinary: config.Git.Binary, AllowedRoot: config.Git.AllowedRoot, OperationTimeout: gitOperationTimeout}); providerErr != nil {
			return resolvedProductionConfig{}, invalidConfig("Git release-provider root or binary is invalid")
		}
	}
	var federationRegistry *organization.StaticFederationRegistry
	var federationPrivateKey ed25519.PrivateKey
	var federationTimeout, federationFutureSkew, federationTTL time.Duration
	if config.Federation != nil {
		federationTimeout, err = positiveDuration("federation.request_timeout", config.Federation.RequestTimeout)
		if err != nil {
			return resolvedProductionConfig{}, err
		}
		federationFutureSkew, err = positiveDuration("federation.allowed_future_skew", config.Federation.AllowedFutureSkew)
		if err != nil || federationFutureSkew > time.Minute {
			return resolvedProductionConfig{}, invalidConfig("federation future-skew policy is invalid")
		}
		federationTTL, err = positiveDuration("federation.envelope_ttl", config.Federation.EnvelopeTTL)
		if err != nil || federationTTL > organization.MaximumFederationTTL || !loopbackAddress(config.Federation.Address) || config.Federation.Address == config.Operator.Address || config.Federation.MaximumBodyBytes <= 0 || config.Federation.MaximumBodyBytes > federationhttp.DefaultMaximumBodyBytes || config.Federation.SigningIdentity.Validate() != nil || config.Federation.SigningIdentity.DeploymentIdentity != config.DeploymentIdentity || config.Federation.SigningIdentity.Status != organization.FederationActive {
			return resolvedProductionConfig{}, invalidConfig("federation identity, listener, or limits are invalid")
		}
		privateRaw, readErr := os.ReadFile(config.Federation.PrivateKeyFile)
		if readErr != nil {
			return resolvedProductionConfig{}, invalidConfig("federation private key file is missing")
		}
		decoded, decodeErr := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(privateRaw)))
		if decodeErr != nil || len(decoded) != ed25519.PrivateKeySize {
			return resolvedProductionConfig{}, invalidConfig("federation private key is invalid")
		}
		federationPrivateKey = ed25519.PrivateKey(decoded)
		signingPublicKey, publicKeyErr := config.Federation.SigningIdentity.PublicEd25519Key()
		if publicKeyErr != nil || !federationPrivateKey.Public().(ed25519.PublicKey).Equal(signingPublicKey) {
			return resolvedProductionConfig{}, invalidConfig("federation private key does not match signing identity")
		}
		grants := append([]organization.FederationTrustGrant{config.Federation.SigningIdentity}, config.Federation.TrustGrants...)
		federationRegistry, err = organization.NewStaticFederationRegistry(config.Federation.Aliases, config.Federation.Routes, grants)
		if err != nil {
			return resolvedProductionConfig{}, invalidConfig("federation aliases, routes, or trust grants are invalid")
		}
		for _, route := range config.Federation.Routes {
			if route.SourceDeployment != config.DeploymentIdentity && route.DestinationDeployment != config.DeploymentIdentity {
				return resolvedProductionConfig{}, invalidConfig("federation route does not involve this deployment")
			}
		}
	}
	if config.OpenHands.MaximumPages == 0 || config.OpenHands.MaximumPages > 1000 || config.OpenHands.MaximumEvidenceBytes <= 0 || config.OpenHands.MaximumEvidenceBytes > 16<<20 || config.Execution.ConsumerID == "" || len(config.Execution.ConsumerID) > 256 || config.Execution.MaximumBriefBytes <= 0 || config.Execution.MaximumBriefBytes > 1<<20 || config.Execution.PolicyRevision == 0 || config.Evidence.PolicyRevision == 0 || config.Evidence.ProducingVersion == "" || config.Evidence.RetentionPolicy == "" || config.Worker.MaximumReconciliations == 0 || config.Worker.MaximumConcurrentInvocations == 0 || config.Worker.MaximumConcurrentInvocations > 64 || reconciliation >= leaseDuration {
		return resolvedProductionConfig{}, invalidConfig("execution, evidence, OpenHands, or worker limits are invalid")
	}
	if config.ServiceAuthority.Kind != kernel.PrincipalService || !config.ServiceAuthority.Valid() || config.ExpiryAuthority.Kind != kernel.PrincipalPolicy || !config.ExpiryAuthority.Valid() {
		return resolvedProductionConfig{}, invalidConfig("service and expiry authorities are invalid")
	}
	workspaceBindings := make([]openhands.WorkspaceBinding, 0, len(config.Workspaces))
	for _, workspace := range config.Workspaces {
		info, statErr := os.Stat(workspace.WorkingDirectory)
		if workspace.WorkspaceID == "" || workspace.WorktreeID == "" || !filepath.IsAbs(workspace.WorkingDirectory) || workspace.Branch == "" || len(workspace.BaselineSHA) != 40 || len(workspace.WritablePaths) == 0 || statErr != nil || !info.IsDir() {
			return resolvedProductionConfig{}, invalidConfig("workspace binding is invalid or missing")
		}
		workspaceBindings = append(workspaceBindings, openhands.WorkspaceBinding{WorkspaceID: workspace.WorkspaceID, WorktreeID: workspace.WorktreeID, WorkingDirectory: workspace.WorkingDirectory})
	}
	if _, err := openhands.NewBoundWorkspaceResolver(workspaceBindings); err != nil {
		return resolvedProductionConfig{}, invalidConfig("workspace bindings are not unique and exact")
	}
	profiles := make([]openhands.ExecutionProfile, 0, len(config.Profiles))
	for _, profile := range config.Profiles {
		resolved, profileErr := openhands.NewAcceptedExecutionProfile(profile.ModelProfileDigest, profile.RuntimeIdentityDigest, profile.ToolPolicyDigest, profile.EffectPolicyDigest, profile.MaximumIterations, config.TeamsDatabaseIdentity, config.SMADatabaseIdentity)
		if profileErr != nil || !profile.Qualification.Valid() || profile.Qualification.ModelProfileDigest != profile.ModelProfileDigest {
			return resolvedProductionConfig{}, invalidConfig("execution profile is invalid")
		}
		profiles = append(profiles, resolved)
	}
	if _, err := openhands.NewBoundExecutionProfileResolver(profiles); err != nil {
		return resolvedProductionConfig{}, invalidConfig("execution profiles are not unique and exact")
	}
	trustedKeys := make(map[string]ed25519.PublicKey, len(config.Organization.Publishers))
	for _, publisher := range config.Organization.Publishers {
		if publisher.KeyID == "" || publisher.PublicKeyFile == "" || trustedKeys[publisher.KeyID] != nil {
			return resolvedProductionConfig{}, invalidConfig("trusted role publishers are invalid or duplicated")
		}
		raw, readErr := os.ReadFile(publisher.PublicKeyFile)
		if readErr != nil {
			return resolvedProductionConfig{}, invalidConfig("trusted role publisher key is missing")
		}
		decoded, decodeErr := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(raw)))
		if decodeErr != nil || len(decoded) != ed25519.PublicKeySize {
			return resolvedProductionConfig{}, invalidConfig("trusted role publisher key is invalid")
		}
		trustedKeys[publisher.KeyID] = ed25519.PublicKey(decoded)
	}
	team, err := organization.LoadTeamManifest(config.Organization.ManifestFile, config.Organization.ManifestDigest, trustedKeys)
	if err != nil {
		return resolvedProductionConfig{}, invalidConfig("team manifest or role bundle is invalid")
	}
	if config.Federation != nil {
		for _, route := range config.Federation.Routes {
			if route.SourceDeployment == config.DeploymentIdentity && !loadedTeamHasActor(team, route.SourceActor) || route.DestinationDeployment == config.DeploymentIdentity && !loadedTeamHasActor(team, route.DestinationActor) {
				return resolvedProductionConfig{}, invalidConfig("federation route local actor is not in the exact team roster")
			}
		}
		for _, alias := range config.Federation.Aliases {
			route, found, routeErr := federationRegistry.LoadRoute(context.Background(), alias.RouteID, alias.RouteRevision)
			if routeErr != nil || !found || route.SourceDeployment != config.DeploymentIdentity {
				return resolvedProductionConfig{}, invalidConfig("federation alias is not an outbound route for this deployment")
			}
		}
	}
	libraryTeams := make([]organization.LoadedTeam, 0, len(config.Organization.LibraryManifests))
	for _, source := range config.Organization.LibraryManifests {
		loaded, loadErr := organization.LoadTeamManifest(source.ManifestFile, source.ManifestDigest, trustedKeys)
		if loadErr != nil {
			return resolvedProductionConfig{}, invalidConfig("role-library manifest or bundle is invalid")
		}
		libraryTeams = append(libraryTeams, loaded)
	}
	var policy kernel.AuthorizationPolicy
	if err := readStrictJSONFile(config.AuthorizationPolicyFile, &policy); err != nil || !productionPolicyValid(policy, config.ServiceAuthority, config.ExpiryAuthority) || config.Execution.PolicyRevision != policy.Revision || config.Evidence.PolicyRevision != policy.Revision {
		return resolvedProductionConfig{}, invalidConfig("authorization policy file is invalid")
	}
	var provenance kernel.ProvenanceBasis
	if err := readStrictJSONFile(config.ProvenanceFile, &provenance); err != nil || !provenance.Valid() || provenance.PolicyDigest != policy.PolicyDigest || provenance.PolicyRevision != policy.Revision {
		return resolvedProductionConfig{}, invalidConfig("provenance file is invalid or does not bind the authorization policy")
	}
	return resolvedProductionConfig{ProductionConfig: config, mongoURI: mongoURI, sessionAPIKey: sessionKey, operatorBearerToken: operatorToken, humanCredentials: humanCredentials, authorizationPolicy: policy, provenance: provenance, requestTimeout: requestTimeout, pollInterval: pollInterval, operationTimeout: operationTimeout, leaseDuration: leaseDuration, reconciliation: reconciliation, leaseOperationTimeout: leaseOperationTimeout, projectionInterval: projectionInterval, projectionTimeout: projectionTimeout, operatorTimeout: operatorTimeout, team: team, libraryTeams: libraryTeams, trustedRolePublishers: trustedKeys, roleReconciliation: roleReconciliation, planningDeadline: planningDeadline, gitOperationTimeout: gitOperationTimeout, federationRegistry: federationRegistry, federationPrivateKey: federationPrivateKey, federationTimeout: federationTimeout, federationFutureSkew: federationFutureSkew, federationTTL: federationTTL}, nil
}

func loadedTeamHasActor(team organization.LoadedTeam, actor kernel.ActorFQN) bool {
	for _, loaded := range team.Roles {
		for instance := uint32(1); instance <= loaded.Binding.MaximumInstances; instance++ {
			expected, err := kernel.ParseActorFQN(fmt.Sprintf("%s::%s-%d", team.Manifest.Team, loaded.Binding.Role, instance))
			if err == nil && expected == actor {
				return true
			}
		}
	}
	return false
}

// ProductionService owns the Mongo store, assembled runtime, and lifecycle
// controller created from one validated production configuration.
type ProductionService struct {
	Store              *mongo.Store
	Runtime            *Runtime
	Controller         *Controller
	RoleHost           *organization.Host
	RoleRuntime        *organization.InProcessRuntime
	MessageBus         *organization.MessageBus
	RoleInbox          *organization.RoleInbox
	RoleLibrary        *organization.RoleLibrary
	FederationRegistry *organization.StaticFederationRegistry
	FederationIngress  *organization.FederationIngress
	Federation         *organization.FederationCoordinator
	Features           *organization.FeatureCoordinator
	Releases           *application.ReleaseCoordinator

	projectionInterval     time.Duration
	projectionTimeout      time.Duration
	recoveryInterval       time.Duration
	recoveryTimeout        time.Duration
	recoveryAttempts       uint32
	mu                     sync.Mutex
	projectionCancel       context.CancelFunc
	projectionDone         chan error
	recoveryDone           chan error
	roleRecoveryDone       chan error
	failures               chan error
	provenance             kernel.ProvenanceBasis
	operatorToken          string
	operatorIdentity       protocol.AuthenticatedContext
	humanCredentials       []HumanTransportCredential
	operatorTimeout        time.Duration
	operatorMaxBody        int64
	roleReconciliation     time.Duration
	roleMaximumRestarts    uint32
	messageMaximumAttempts uint32
	faultMu                sync.Mutex
	recoveryFaults         map[string]RecoveryFault
	planningDeadline       time.Duration
	planning               ProductionPlanning
	profilesByModel        map[kernel.Digest]ProductionProfile
	workspacesByID         map[string]ProductionWorkspace
	serviceAuthority       kernel.PrincipalRef
	policyAuthority        kernel.PrincipalRef
	clock                  kernel.Clock
	ids                    kernel.IDSource
	librarySources         []ProductionTeamSource
	trustedRolePublishers  map[string]ed25519.PublicKey
	federationAddress      string
	federationMaximumBody  int64
}

func NewProductionService(ctx context.Context, config ProductionConfig) (*ProductionService, error) {
	resolved, err := resolveProductionConfig(config)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(config.EvidenceRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create evidence root: %w", err)
	}
	if info, err := os.Stat(config.EvidenceRoot); err != nil || !info.IsDir() {
		return nil, invalidConfig("evidence root is not a directory")
	}
	store, err := mongo.Open(ctx, mongo.Config{URI: resolved.mongoURI, Database: config.Mongo.Database, ContractIdentity: kernel.ContractIdentity, ManifestSHA256: ManifestSHA256, MigrationLevel: 1, Policy: resolved.authorizationPolicy, BacklogLimit: config.Mongo.BacklogLimit, DeliveryPolicyRevision: config.Mongo.DeliveryPolicyRevision, DeploymentIdentity: config.DeploymentIdentity})
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*ProductionService, error) {
		_ = store.Close(context.WithoutCancel(ctx))
		return nil, cause
	}
	catalogue, err := contract.Load(os.DirFS(config.ContractRoot), ContractPackagePath)
	if err != nil {
		return fail(err)
	}
	clock := SystemClock{}
	ids, err := NewUUIDv7Source(clock)
	if err != nil {
		return fail(err)
	}
	workspaces := make([]openhands.WorkspaceBinding, 0, len(config.Workspaces))
	for _, item := range config.Workspaces {
		workspaces = append(workspaces, openhands.WorkspaceBinding{WorkspaceID: item.WorkspaceID, WorktreeID: item.WorktreeID, WorkingDirectory: item.WorkingDirectory})
	}
	profiles := make([]openhands.ExecutionProfile, 0, len(config.Profiles))
	profilesByModel := make(map[kernel.Digest]ProductionProfile, len(config.Profiles))
	for _, item := range config.Profiles {
		profile, profileErr := openhands.NewAcceptedExecutionProfile(item.ModelProfileDigest, item.RuntimeIdentityDigest, item.ToolPolicyDigest, item.EffectPolicyDigest, item.MaximumIterations, config.TeamsDatabaseIdentity, config.SMADatabaseIdentity)
		if profileErr != nil {
			return fail(profileErr)
		}
		profiles = append(profiles, profile)
		profilesByModel[item.ModelProfileDigest] = item
	}
	runtime, err := New(ctx, Config{
		Store: store, Catalogue: catalogue, Clock: clock, IDs: ids,
		OpenHandsBaseURL: config.OpenHands.BaseURL, OpenHandsSessionAPIKey: resolved.sessionAPIKey,
		HTTPClient: &http.Client{Timeout: resolved.requestTimeout}, WorkspaceBindings: workspaces, ExecutionProfiles: profiles,
		OpenHandsPollInterval: resolved.pollInterval, OpenHandsMaximumPages: config.OpenHands.MaximumPages,
		OpenHandsMaximumEvidence: config.OpenHands.MaximumEvidenceBytes, EvidenceRoot: config.EvidenceRoot,
		ExecutionPolicy: application.OperationalExecutionPolicy{OperationTimeout: resolved.operationTimeout, MaximumBriefBytes: config.Execution.MaximumBriefBytes, ConsumerID: config.Execution.ConsumerID, PolicyRevision: config.Execution.PolicyRevision, ServiceAuthority: config.ServiceAuthority, ExpiryAuthority: config.ExpiryAuthority, Provenance: resolved.provenance},
		EvidencePolicy:  application.CommandEvidenceRecorderPolicy{PolicyRevision: config.Evidence.PolicyRevision, Authority: config.ServiceAuthority, Provenance: resolved.provenance, ProducingVersion: config.Evidence.ProducingVersion, RetentionPolicy: config.Evidence.RetentionPolicy},
		WorkerPolicy:    executionruntime.Policy{ConsumerID: config.Execution.ConsumerID, LeaseDuration: resolved.leaseDuration, ReconciliationInterval: resolved.reconciliation, MaximumReconciliations: config.Worker.MaximumReconciliations, MaximumConcurrentInvocations: config.Worker.MaximumConcurrentInvocations, LeaseOperationTimeout: resolved.leaseOperationTimeout},
	})
	if err != nil {
		return fail(err)
	}
	controller, err := NewController(runtime, clock)
	if err != nil {
		_ = runtime.Close(context.WithoutCancel(ctx))
		return fail(err)
	}
	roleInbox := organization.NewRoleInbox()
	messageBus, err := organization.NewMessageBus(store, store)
	if err != nil {
		_ = runtime.Close(context.WithoutCancel(ctx))
		return fail(err)
	}
	roleRuntime, err := organization.NewInProcessRuntime(&organizationalRoleWorker{store: store, inbox: roleInbox, pollInterval: resolved.pollInterval, openTimeout: resolved.leaseOperationTimeout})
	if err != nil {
		_ = runtime.Close(context.WithoutCancel(ctx))
		return fail(err)
	}
	roleHost, err := organization.NewHost(resolved.team, store, roleRuntime, clock, ids)
	if err != nil {
		_ = runtime.Close(context.WithoutCancel(ctx))
		return fail(err)
	}
	libraryTeams := append([]organization.LoadedTeam{resolved.team}, resolved.libraryTeams...)
	roleLibrary, err := organization.NewRoleLibrary(libraryTeams...)
	if err != nil {
		_ = runtime.Close(context.WithoutCancel(ctx))
		return fail(err)
	}
	workspacesByID := make(map[string]ProductionWorkspace, len(config.Workspaces))
	for _, workspace := range config.Workspaces {
		workspacesByID[workspace.WorkspaceID] = workspace
	}
	librarySources := append([]ProductionTeamSource{{ManifestFile: config.Organization.ManifestFile, ManifestDigest: config.Organization.ManifestDigest}}, config.Organization.LibraryManifests...)
	trustedPublishers := make(map[string]ed25519.PublicKey, len(resolved.trustedRolePublishers))
	for key, value := range resolved.trustedRolePublishers {
		trustedPublishers[key] = append(ed25519.PublicKey(nil), value...)
	}
	service := &ProductionService{Store: store, Runtime: runtime, Controller: controller, RoleHost: roleHost, RoleRuntime: roleRuntime, MessageBus: messageBus, RoleInbox: roleInbox, RoleLibrary: roleLibrary, projectionInterval: resolved.projectionInterval, projectionTimeout: resolved.projectionTimeout, recoveryInterval: resolved.reconciliation, recoveryTimeout: resolved.leaseOperationTimeout, recoveryAttempts: config.Worker.MaximumReconciliations, failures: make(chan error, 4), provenance: resolved.provenance, operatorToken: resolved.operatorBearerToken, operatorIdentity: protocol.AuthenticatedContext{Principal: config.Operator.Principal}, humanCredentials: append([]HumanTransportCredential(nil), resolved.humanCredentials...), operatorTimeout: resolved.operatorTimeout, operatorMaxBody: config.Operator.MaximumBodyBytes, roleReconciliation: resolved.roleReconciliation, roleMaximumRestarts: config.Organization.MaximumRestarts, messageMaximumAttempts: config.Organization.MaximumDeliveryAttempts, recoveryFaults: make(map[string]RecoveryFault), clock: clock, ids: ids, planningDeadline: resolved.planningDeadline, planning: config.Planning, profilesByModel: profilesByModel, workspacesByID: workspacesByID, serviceAuthority: config.ServiceAuthority, policyAuthority: config.ExpiryAuthority, librarySources: librarySources, trustedRolePublishers: trustedPublishers}
	if config.Federation != nil {
		federationIngress, ingressErr := organization.NewFederationIngress(resolved.federationRegistry, store, config.DeploymentIdentity, resolved.federationFutureSkew)
		if ingressErr != nil {
			return fail(ingressErr)
		}
		federationClient, clientErr := federationhttp.NewClient(&http.Client{Timeout: resolved.federationTimeout}, config.Federation.MaximumBodyBytes)
		if clientErr != nil {
			return fail(clientErr)
		}
		federationCoordinator, coordinatorErr := organization.NewFederationCoordinator(resolved.federationRegistry, store, store, federationClient, clock, ids, config.DeploymentIdentity, config.Federation.SigningIdentity, resolved.federationPrivateKey, resolved.federationTTL)
		if coordinatorErr != nil {
			return fail(coordinatorErr)
		}
		service.FederationRegistry = resolved.federationRegistry
		service.FederationIngress = federationIngress
		service.Federation = federationCoordinator
		service.federationAddress = config.Federation.Address
		service.federationMaximumBody = config.Federation.MaximumBodyBytes
	}
	features, err := organization.NewFeatureCoordinator(store, productionFeatureRoleHost{service: service}, service, clock, ids)
	if err != nil {
		return fail(err)
	}
	service.Features = features
	if config.Git != nil {
		provider, providerErr := gitprovider.New(gitprovider.Config{GitBinary: config.Git.Binary, AllowedRoot: config.Git.AllowedRoot, OperationTimeout: resolved.gitOperationTimeout})
		if providerErr != nil {
			_ = service.Close(ctx)
			return nil, providerErr
		}
		releases, releaseErr := application.NewReleaseCoordinator(runtime, provider, application.ReleaseCoordinatorPolicy{OperationTimeout: resolved.gitOperationTimeout})
		if releaseErr != nil {
			_ = service.Close(ctx)
			return nil, releaseErr
		}
		service.Releases = releases
	}
	return service, nil
}

func (service *ProductionService) Submit(ctx context.Context, command kernel.KernelCommand) (kernel.CommandReceipt, error) {
	if service == nil || service.Runtime == nil || !service.provenance.Valid() {
		return kernel.CommandReceipt{}, application.ErrInvalidConfiguration
	}
	return service.Runtime.Handle(ctx, command, service.provenance)
}

func (service *ProductionService) Status() ControlStatus {
	if service == nil || service.Controller == nil {
		return ControlStatus{State: ControlFailed, LastError: application.ErrInvalidConfiguration.Error()}
	}
	return service.Controller.Inspect()
}

func (service *ProductionService) Pause(ctx context.Context) error {
	if service == nil || service.Controller == nil {
		return application.ErrInvalidConfiguration
	}
	return service.Controller.Pause(ctx)
}

func (service *ProductionService) Resume(ctx context.Context) error {
	if service == nil || service.Controller == nil {
		return application.ErrInvalidConfiguration
	}
	return service.Controller.Resume(ctx)
}

func (service *ProductionService) ReadTask(ctx context.Context, id kernel.UUIDv7) (mongo.TaskProjection, bool, error) {
	if service == nil || service.Store == nil {
		return mongo.TaskProjection{}, false, application.ErrInvalidConfiguration
	}
	return service.Store.ReadTaskProjection(ctx, id)
}

func (service *ProductionService) ReadStory(ctx context.Context, id kernel.UUIDv7) (mongo.StoryProjection, bool, error) {
	if service == nil || service.Store == nil {
		return mongo.StoryProjection{}, false, application.ErrInvalidConfiguration
	}
	return service.Store.ReadStoryProjection(ctx, id)
}

type InvocationStatus struct {
	InvocationID            kernel.UUIDv7               `json:"invocation_id"`
	Revision                uint64                      `json:"revision"`
	State                   kernel.WorkInvocationState  `json:"state"`
	TaskID                  kernel.UUIDv7               `json:"task_id"`
	BudgetAccountID         kernel.UUIDv7               `json:"budget_account_id"`
	LifecycleEpoch          uint64                      `json:"lifecycle_epoch"`
	ScopeRevision           uint64                      `json:"scope_revision"`
	ActorFQN                kernel.ActorFQN             `json:"actor_fqn"`
	Execution               kernel.ExecutionTuple       `json:"execution"`
	Purpose                 kernel.WorkPurpose          `json:"purpose"`
	AttemptOrdinal          uint64                      `json:"attempt_ordinal"`
	ConversationID          *string                     `json:"conversation_id"`
	TerminalOutcome         *kernel.WorkInvocationState `json:"terminal_outcome"`
	Retryable               *bool                       `json:"retryable"`
	TerminalEvidenceIDs     []kernel.UUIDv7             `json:"terminal_evidence_ids"`
	OutputDigest            *kernel.Digest              `json:"output_digest"`
	FinishedAt              *time.Time                  `json:"finished_at"`
	CancellationRequestedAt *time.Time                  `json:"cancellation_requested_at"`
	LastEventID             kernel.UUIDv7               `json:"last_event_id"`
}

func (service *ProductionService) ReadInvocation(ctx context.Context, id kernel.UUIDv7) (InvocationStatus, bool, error) {
	if service == nil || service.Store == nil || !id.Valid() {
		return InvocationStatus{}, false, application.ErrInvalidConfiguration
	}
	current, err := service.Store.LoadOperationalExecution(ctx, id)
	if errors.Is(err, application.ErrInvalidOperationalExecution) {
		return InvocationStatus{}, false, nil
	}
	if err != nil {
		return InvocationStatus{}, false, err
	}
	invocation := current.Invocation
	return InvocationStatus{
		InvocationID: invocation.ID, Revision: invocation.Revision, State: invocation.State,
		TaskID: invocation.TaskID, BudgetAccountID: invocation.BudgetAccountID,
		LifecycleEpoch: invocation.LifecycleEpoch, ScopeRevision: invocation.ScopeRevision,
		ActorFQN: invocation.ActorFQN, Execution: invocation.Execution, Purpose: invocation.Purpose,
		AttemptOrdinal: invocation.AttemptOrdinal, ConversationID: invocation.ConversationID,
		TerminalOutcome: invocation.TerminalOutcome, Retryable: invocation.Retryable,
		TerminalEvidenceIDs: append([]kernel.UUIDv7(nil), invocation.TerminalEvidenceIDs...), OutputDigest: invocation.OutputDigest,
		FinishedAt: invocation.FinishedAt, CancellationRequestedAt: invocation.CancellationRequestedAt,
		LastEventID: invocation.LastEventID,
	}, true, nil
}

func (service *ProductionService) OperatorCredentials() (string, time.Duration, int64) {
	if service == nil {
		return "", 0, 0
	}
	return service.operatorToken, service.operatorTimeout, service.operatorMaxBody
}

func (service *ProductionService) OperatorIdentity() protocol.AuthenticatedContext {
	if service == nil {
		return protocol.AuthenticatedContext{}
	}
	return service.operatorIdentity
}

func (service *ProductionService) HumanTransportCredentials() []HumanTransportCredential {
	if service == nil {
		return nil
	}
	return append([]HumanTransportCredential(nil), service.humanCredentials...)
}

func (service *ProductionService) RoleRoster(ctx context.Context) ([]organization.RoleInstanceState, error) {
	if service == nil || service.RoleHost == nil {
		return nil, application.ErrInvalidConfiguration
	}
	return service.RoleHost.Roster(ctx)
}

func (service *ProductionService) StartRole(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	if service == nil || service.RoleHost == nil {
		return organization.RoleInstanceState{}, application.ErrInvalidConfiguration
	}
	state, err := service.RoleHost.EnsureStarted(ctx, actor)
	if err != nil {
		return state, err
	}
	if err := service.registerRoleState(ctx, state); err != nil {
		return state, err
	}
	return state, nil
}

func (service *ProductionService) StopRole(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	if service == nil || service.RoleHost == nil {
		return organization.RoleInstanceState{}, application.ErrInvalidConfiguration
	}
	return service.RoleHost.Stop(ctx, actor)
}

func (service *ProductionService) RestartRole(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	if service == nil || service.RoleHost == nil {
		return organization.RoleInstanceState{}, application.ErrInvalidConfiguration
	}
	state, err := service.RoleHost.Restart(ctx, actor)
	if err != nil {
		return state, err
	}
	if err := service.registerRoleState(ctx, state); err != nil {
		return state, err
	}
	return state, nil
}

func (service *ProductionService) PauseRole(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	if service == nil || service.RoleHost == nil {
		return organization.RoleInstanceState{}, application.ErrInvalidConfiguration
	}
	return service.RoleHost.Pause(ctx, actor)
}

func (service *ProductionService) ResumeRole(ctx context.Context, actor kernel.ActorFQN) (organization.RoleInstanceState, error) {
	if service == nil || service.RoleHost == nil {
		return organization.RoleInstanceState{}, application.ErrInvalidConfiguration
	}
	return service.RoleHost.Resume(ctx, actor)
}

func (service *ProductionService) SendMessage(ctx context.Context, message organization.OrganizationalMessage) error {
	if service == nil || service.MessageBus == nil {
		return application.ErrInvalidConfiguration
	}
	return service.MessageBus.Send(ctx, message)
}

func (service *ProductionService) SendMessageFanout(ctx context.Context, messages []organization.OrganizationalMessage) error {
	if service == nil || service.MessageBus == nil {
		return application.ErrInvalidConfiguration
	}
	return service.MessageBus.SendFanout(ctx, messages)
}

type FederationSnapshot struct {
	Configured bool                                `json:"configured"`
	Aliases    []organization.AliasBinding         `json:"aliases"`
	Routes     []organization.FederationRoute      `json:"routes"`
	Trust      []organization.FederationTrustGrant `json:"trust_grants"`
}

func (service *ProductionService) FederationSnapshot() FederationSnapshot {
	if service == nil || service.FederationRegistry == nil {
		return FederationSnapshot{}
	}
	return FederationSnapshot{Configured: true, Aliases: service.FederationRegistry.Aliases(), Routes: service.FederationRegistry.Routes(), Trust: service.FederationRegistry.TrustGrants()}
}

func (service *ProductionService) ResolveFederationAlias(ctx context.Context, name string) (organization.AliasBinding, organization.FederationRoute, error) {
	if service == nil || service.Federation == nil {
		return organization.AliasBinding{}, organization.FederationRoute{}, application.ErrInvalidConfiguration
	}
	return service.Federation.ResolveAlias(ctx, name)
}

func (service *ProductionService) SendFederatedMessage(ctx context.Context, alias string, message organization.OrganizationalMessage) (organization.FederationDeliveryReceipt, error) {
	if service == nil || service.Federation == nil {
		return organization.FederationDeliveryReceipt{}, application.ErrInvalidConfiguration
	}
	return service.Federation.SendByAlias(ctx, alias, message)
}

func (service *ProductionService) FederationListener() (string, int64, *organization.FederationIngress) {
	if service == nil {
		return "", 0, nil
	}
	return service.federationAddress, service.federationMaximumBody, service.FederationIngress
}

func (service *ProductionService) RoleInboxSnapshot(actor kernel.ActorFQN) []organization.OrganizationalMessage {
	if service == nil || service.RoleInbox == nil {
		return nil
	}
	return service.RoleInbox.Snapshot(actor)
}

func (service *ProductionService) ReadMessage(ctx context.Context, id kernel.UUIDv7) (organization.MessageClaim, bool, error) {
	if service == nil || service.MessageBus == nil {
		return organization.MessageClaim{}, false, application.ErrInvalidConfiguration
	}
	return service.MessageBus.Read(ctx, id)
}

func (service *ProductionService) TraceMessages(ctx context.Context, thread kernel.UUIDv7) ([]organization.MessageClaim, error) {
	if service == nil || service.MessageBus == nil {
		return nil, application.ErrInvalidConfiguration
	}
	return service.MessageBus.Trace(ctx, thread)
}

func (service *ProductionService) DeadLetters(ctx context.Context, recipient kernel.ActorFQN, limit int64) ([]organization.MessageClaim, error) {
	if service == nil || service.MessageBus == nil {
		return nil, application.ErrInvalidConfiguration
	}
	return service.MessageBus.DeadLetters(ctx, recipient, limit)
}

func (service *ProductionService) RoleLibraries() []organization.RoleLibraryEntry {
	if service == nil || service.RoleLibrary == nil {
		return nil
	}
	return service.RoleLibrary.List()
}

// SyncRoleLibraries reloads only the exact, digest-bound sources from the
// validated daemon configuration. Callers cannot supply an arbitrary path or
// publisher key through the operator surface.
func (service *ProductionService) SyncRoleLibraries() ([]organization.RoleLibraryEntry, error) {
	if service == nil || service.RoleLibrary == nil || len(service.librarySources) == 0 || len(service.trustedRolePublishers) == 0 {
		return nil, application.ErrInvalidConfiguration
	}
	for _, source := range service.librarySources {
		team, err := organization.LoadTeamManifest(source.ManifestFile, source.ManifestDigest, service.trustedRolePublishers)
		if err != nil {
			return nil, err
		}
		if _, err := service.RoleLibrary.Sync(team); err != nil {
			return nil, err
		}
	}
	return service.RoleLibrary.List(), nil
}

func (service *ProductionService) Start(ctx context.Context) error {
	if service == nil {
		return application.ErrInvalidConfiguration
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.Controller == nil || service.Store == nil || service.RoleHost == nil || service.projectionCancel != nil || service.recoveryDone != nil || service.roleRecoveryDone != nil {
		return application.ErrInvalidConfiguration
	}
	if err := service.Controller.Start(ctx); err != nil {
		return err
	}
	if err := service.reconcileDurableRoleExecutions(ctx); err != nil {
		_ = service.Controller.Stop(context.WithoutCancel(ctx))
		return fmt.Errorf("reconcile durable role executions: %w", err)
	}
	eager, err := service.RoleHost.StartEager(ctx)
	if err != nil {
		_ = service.Controller.Stop(context.WithoutCancel(ctx))
		return fmt.Errorf("start eager roles: %w", err)
	}
	for _, state := range eager {
		if err := service.registerRoleState(ctx, state); err != nil {
			_ = service.Controller.Stop(context.WithoutCancel(ctx))
			return fmt.Errorf("register eager role %s: %w", state.ActorFQN, err)
		}
	}
	projectionContext, cancel := context.WithCancel(context.Background())
	service.projectionCancel = cancel
	service.projectionDone = make(chan error, 1)
	service.recoveryDone = make(chan error, 1)
	service.roleRecoveryDone = make(chan error, 1)
	go func() {
		err := service.runProjector(projectionContext)
		if err != nil && !errors.Is(err, context.Canceled) {
			service.failures <- err
		}
		service.projectionDone <- err
	}()
	go func() {
		err := service.runExpiredLeaseRecovery(projectionContext)
		if err != nil && !errors.Is(err, context.Canceled) {
			service.failures <- err
		}
		service.recoveryDone <- err
	}()
	go func() {
		err := service.runRoleRecovery(projectionContext)
		if err != nil && !errors.Is(err, context.Canceled) {
			service.failures <- err
		}
		service.roleRecoveryDone <- err
	}()
	go func() {
		select {
		case err := <-service.Controller.Failures():
			service.failures <- err
		case <-projectionContext.Done():
		}
	}()
	return nil
}

func (service *ProductionService) Failures() <-chan error {
	if service == nil {
		return nil
	}
	return service.failures
}

func (service *ProductionService) Stop(ctx context.Context) error {
	if service == nil {
		return application.ErrInvalidConfiguration
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.Controller == nil || service.projectionCancel == nil || service.projectionDone == nil || service.recoveryDone == nil || service.roleRecoveryDone == nil {
		return application.ErrInvalidConfiguration
	}
	service.projectionCancel()
	var result error
	if service.RoleHost != nil {
		roster, err := service.RoleHost.Roster(ctx)
		result = errors.Join(result, err)
		for index := len(roster) - 1; index >= 0; index-- {
			if roster[index].Status == organization.RoleStopped {
				continue
			}
			_, err := service.RoleHost.Stop(ctx, roster[index].ActorFQN)
			result = errors.Join(result, err)
		}
	}
	switch service.Controller.Inspect().State {
	case ControlRunning, ControlPaused, ControlFailed:
		result = errors.Join(result, service.Controller.Stop(ctx))
	}
	select {
	case err := <-service.projectionDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			result = errors.Join(result, err)
		}
	case <-ctx.Done():
		result = errors.Join(result, ctx.Err())
	}
	select {
	case err := <-service.recoveryDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			result = errors.Join(result, err)
		}
	case <-ctx.Done():
		result = errors.Join(result, ctx.Err())
	}
	select {
	case err := <-service.roleRecoveryDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			result = errors.Join(result, err)
		}
	case <-ctx.Done():
		result = errors.Join(result, ctx.Err())
	}
	service.projectionCancel = nil
	service.projectionDone = nil
	service.recoveryDone = nil
	service.roleRecoveryDone = nil
	return result
}

func (service *ProductionService) runProjector(ctx context.Context) error {
	for {
		operationContext, cancel := context.WithTimeout(ctx, service.projectionTimeout)
		_, err := service.Store.ProjectPendingOperationalEvents(operationContext, time.Now().UTC())
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("project operational events: %w", err)
		}
		timer := time.NewTimer(service.projectionInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// runExpiredLeaseRecovery makes process restart and host sleep/wake recovery
// explicit. An invocation claimed by a process that disappeared is returned to
// PENDING after its durable lease expires, which causes the existing Mongo
// change-stream feed to redeliver it. Repeated abandoned claims are bounded by
// the configured attempt limit and become observable dead letters.
func (service *ProductionService) runExpiredLeaseRecovery(ctx context.Context) error {
	return runExpiredLeaseRecovery(ctx, service.Store, time.Now, service.recoveryInterval, service.recoveryTimeout, service.recoveryAttempts)
}

func (service *ProductionService) runRoleRecovery(ctx context.Context) error {
	if service.RoleHost == nil || service.roleReconciliation <= 0 || service.roleMaximumRestarts == 0 {
		return application.ErrInvalidConfiguration
	}
	attempts := make(map[kernel.ActorFQN]uint32)
	for {
		operationContext, cancel := context.WithTimeout(ctx, service.recoveryTimeout)
		_, _, err := service.MessageBus.Sweep(operationContext, time.Now().UTC(), service.messageMaximumAttempts)
		var roster []organization.RoleInstanceState
		if err == nil {
			roster, err = service.RoleHost.Roster(operationContext)
		}
		if err == nil {
			for _, role := range roster {
				if role.Status == organization.RoleStopped {
					delete(attempts, role.ActorFQN)
					continue
				}
				if attempts[role.ActorFQN] >= service.roleMaximumRestarts {
					err = fmt.Errorf("role %s exceeded restart limit %d", role.ActorFQN, service.roleMaximumRestarts)
					break
				}
				state, restarted, reconcileErr := service.RoleHost.Reconcile(operationContext, role.ActorFQN)
				if reconcileErr != nil {
					err = reconcileErr
					break
				}
				if registerErr := service.registerRoleState(operationContext, state); registerErr != nil {
					err = registerErr
					break
				}
				if restarted {
					attempts[role.ActorFQN]++
				} else if !role.StartedAt.IsZero() && time.Since(role.StartedAt) >= 3*service.roleReconciliation {
					attempts[role.ActorFQN] = 0
				}
			}
		}
		if err == nil {
			if planningErr := service.reconcileFeaturePlanning(operationContext); planningErr != nil {
				service.recordRecoveryFault("feature-planning", planningErr)
			} else {
				service.clearRecoveryFault("feature-planning")
			}
			if workErr := service.reconcilePlannedFeatureWork(operationContext); workErr != nil {
				service.recordRecoveryFault("feature-work", workErr)
			} else {
				service.clearRecoveryFault("feature-work")
			}
		}
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("recover role runtime: %w", err)
		}
		timer := time.NewTimer(service.roleReconciliation)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type expiredIntentSweeper interface {
	SweepExpiredIntents(context.Context, time.Time, uint32) (mongo.SweepResult, error)
}

func runExpiredLeaseRecovery(ctx context.Context, sweeper expiredIntentSweeper, now func() time.Time, interval, timeout time.Duration, maximumAttempts uint32) error {
	if sweeper == nil || now == nil || interval <= 0 || timeout <= 0 || maximumAttempts == 0 {
		return application.ErrInvalidConfiguration
	}
	for {
		operationContext, cancel := context.WithTimeout(ctx, timeout)
		_, err := sweeper.SweepExpiredIntents(operationContext, now().UTC(), maximumAttempts)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("recover expired invocation leases: %w", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (service *ProductionService) Close(ctx context.Context) error {
	if service == nil {
		return nil
	}
	var result error
	service.mu.Lock()
	running := service.projectionCancel != nil
	service.mu.Unlock()
	if running {
		result = errors.Join(result, service.Stop(ctx))
	}
	if service.Runtime != nil {
		result = errors.Join(result, service.Runtime.Close(ctx))
	}
	if service.Store != nil {
		result = errors.Join(result, service.Store.Close(ctx))
	}
	return result
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON document has trailing content")
	}
	return nil
}

func readStrictJSONFile(path string, target any) error {
	if !filepath.IsAbs(path) {
		return errors.New("path is not absolute")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > maximumConfigBytes {
		return errors.New("file is missing or invalid")
	}
	return decodeStrict(raw, target)
}

func readSecret(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("secret path is not absolute")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("secret file must be regular and accessible only by its owner")
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > 16<<10 {
		return "", errors.New("secret file is missing or invalid")
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("secret file must contain exactly one value")
	}
	return value, nil
}

func loopbackHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.TrimRight(parsed.Path, "/") != "" || parsed.Port() == "" {
		return false
	}
	return loopbackHost(parsed.Hostname())
}

func loopbackMongoURI(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "mongodb" || parsed.Hostname() == "" || parsed.Port() == "" || strings.Contains(parsed.Host, ",") {
		return false
	}
	return loopbackHost(parsed.Hostname())
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func loopbackAddress(value string) bool {
	host, port, err := net.SplitHostPort(value)
	return err == nil && port != "" && loopbackHost(host)
}

func positiveDuration(name, value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, invalidConfig(name + " must be a positive duration")
	}
	return duration, nil
}

func absoluteFrom(base, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}

func invalidConfig(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidProductionConfiguration, reason)
}

func productionPolicyValid(policy kernel.AuthorizationPolicy, service, expiry kernel.PrincipalRef) bool {
	if !policy.PolicyDigest.Valid() || policy.Revision == 0 || len(policy.Grants) == 0 {
		return false
	}
	seen := make(map[kernel.Digest]struct{}, len(policy.Grants))
	now := time.Now().UTC()
	for _, grant := range policy.Grants {
		if !grant.GrantDigest.Valid() || !grant.Grantee.Valid() || len(grant.Scope.CommandTypes) == 0 || len(grant.Scope.TargetKinds) == 0 || grant.Revoked || grant.ExpiresAt != nil && !now.Before(*grant.ExpiresAt) {
			return false
		}
		if _, duplicate := seen[grant.GrantDigest]; duplicate {
			return false
		}
		seen[grant.GrantDigest] = struct{}{}
	}
	serviceCommands := []string{"tekroo.command.execution.register", "tekroo.command.execution.replace", "tekroo.command.evidence.register", "tekroo.command.work-invocation.claim", "tekroo.command.work-invocation.record-started", "tekroo.command.work-invocation.record-terminal"}
	for _, command := range serviceCommands {
		if !directGrantCovers(policy.Grants, service, command) {
			return false
		}
	}
	return directGrantCovers(policy.Grants, expiry, "tekroo.command.work-invocation.expire") && directGrantCovers(policy.Grants, expiry, "tekroo.command.work.block")
}

func directGrantCovers(grants []kernel.AuthorityGrant, principal kernel.PrincipalRef, command string) bool {
	for _, grant := range grants {
		if grant.Grantee != principal || grant.Revoked || !grant.Scope.CanReadTarget || grant.Scope.RequiresOwner || len(grant.Scope.TargetIDs) != 0 || !containsProductionString(grant.Scope.CommandTypes, command) {
			continue
		}
		for _, kind := range grant.Scope.TargetKinds {
			if (command == "tekroo.command.execution.register" || command == "tekroo.command.execution.replace") && kind == kernel.AggregateExecution || command == "tekroo.command.evidence.register" && kind == kernel.AggregateEvidence || strings.HasPrefix(command, "tekroo.command.work-invocation.") && kind == kernel.AggregateWorkInvocation || command == "tekroo.command.work.block" && kind == kernel.AggregateTask {
				return true
			}
		}
	}
	return false
}

func containsProductionString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
