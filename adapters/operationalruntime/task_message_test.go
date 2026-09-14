package operationalruntime

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tekroo-ai/teams/kernel"
	"github.com/tekroo-ai/teams/organization"
)

func TestSuccessorPlannedTaskIsBoundToDurableMessageAndHandler(t *testing.T) {
	team := loadStarterSuccessorTeam(t)
	resolver, err := newBoundRoleGroundingResolver(team)
	if err != nil {
		t.Fatal(err)
	}
	roleStore := organization.NewMemoryRoleStore()
	architect := successorRoleState(t, team, "architect", 1, testUUIDValue(970))
	coder := successorRoleState(t, team, "coder", 1, testUUIDValue(971))
	if err := roleStore.CompareAndSwapRole(context.Background(), 0, architect); err != nil {
		t.Fatal(err)
	}
	if err := roleStore.CompareAndSwapRole(context.Background(), 0, coder); err != nil {
		t.Fatal(err)
	}
	messageStore := organization.NewMemoryOrganizationalMessageStore()
	bus, err := organization.NewMessageBus(messageStore, roleStore)
	if err != nil {
		t.Fatal(err)
	}
	service := &ProductionService{
		MessageBus: bus, roleGrounding: resolver,
		planning: ProductionPlanning{TaskMessageRoutes: map[kernel.WorkPurpose]ProductionTaskMessageRoute{
			kernel.PurposeImplementation: {MessageType: "tekroo.message.task.assigned", MessagePurpose: organization.PurposeHandoff},
		}},
	}
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	featureID, storyID, taskID, budgetID := testUUIDValue(972), testUUIDValue(973), testUUIDValue(974), testUUIDValue(975)
	task := organization.PlannedTask{ID: taskID, StoryID: storyID, Title: "Implement bounded change", Description: "Implement it.", AcceptanceCriteria: []string{"it works"}, Owner: coder.ActorFQN, ModelProfile: coder.ModelProfile, DecisionRoute: kernel.RouteBoundedExecution, Purpose: kernel.PurposeImplementation, Complexity: 2, Risk: organization.RiskLow, CriticalPath: true, AttemptLimit: 2, ReviewRoundLimit: 1}
	plan := organization.FeaturePlan{Version: 1, PreparedBy: architect.ActorFQN, PreparedExecution: architect.Execution, Architecture: "One bounded node.", Stories: []organization.PlannedStory{{ID: storyID, Title: "Story", Description: "Deliver it.", AcceptanceCriteria: []string{"it works"}, Priority: organization.PriorityHigh}}, Tasks: []organization.PlannedTask{task}, CreatedAt: now}
	feature := organization.FeatureRequest{ID: featureID, BudgetAccountID: budgetID, LifecycleEpoch: 1, ScopeRevision: 1, Plan: &plan}
	if err := service.ensurePlannedTaskMessages(context.Background(), feature, plan, map[kernel.UUIDv7]kernel.UUIDv7{}, testDigestValue('a'), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	messageID := plannedTaskMessageID(featureID, taskID)
	claim, found, err := bus.Read(context.Background(), messageID)
	if err != nil || !found || claim.State != organization.MessagePending || claim.Message.Recipient != coder.ActorFQN || claim.Message.Type != "tekroo.message.task.assigned" || claim.Message.Flow.MaximumHops != 1 {
		t.Fatalf("message=%#v found=%t err=%v", claim, found, err)
	}
	binding, err := resolver.bindMessageHandler(coder.ActorFQN, claim.Message)
	if err != nil || binding == nil || binding.MessageID != messageID || binding.HandlerDigest == "" {
		t.Fatalf("binding=%#v err=%v", binding, err)
	}
	encodedBinding, err := json.Marshal(binding)
	if err != nil || !strings.Contains(string(encodedBinding), `"allowed_message_proposals":[]`) {
		t.Fatalf("empty proposal allowlist must encode as an array: %s err=%v", encodedBinding, err)
	}
	if err := bus.Admit(context.Background(), messageID, coder.ActorFQN, coder.Execution, testDigestValue('e')); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureTaskMessageAdmitted(context.Background(), messageID, coder.ActorFQN, coder.Execution, testDigestValue('f')); err != nil {
		t.Fatalf("bounded continuation must reuse the task-level message admission: %v", err)
	}
	resolved, found, err := bus.Read(context.Background(), messageID)
	if err != nil || !found || resolved.State != organization.MessageResolved || resolved.Resolution != "WORK_ADMITTED" {
		t.Fatalf("resolved=%#v found=%t err=%v", resolved, found, err)
	}
}

func TestBoundGroundingRetainsInFlightHandlerAcrossAtomicPackageReplacement(t *testing.T) {
	team := loadStarterSuccessorTeam(t)
	resolver, err := newBoundRoleGroundingResolver(team)
	if err != nil {
		t.Fatal(err)
	}
	actor := kernel.ActorFQN(team.Manifest.Team + "::coder-1")
	initial, err := resolver.handlers.Resolve(actor, "tekroo.message.task.assigned", "implementation")
	if err != nil {
		t.Fatal(err)
	}
	initialBinding := handlerBindingFromDispatch(initial, testUUIDValue(990), testDigestValue('9'))

	successor := team
	successor.Manifest.Version = "2.1.0"
	successor.Manifest.Roles = append([]organization.RoleBinding(nil), team.Manifest.Roles...)
	successor.Roles = append([]organization.LoadedRole(nil), team.Roles...)
	successor.Digest = testDigestValue('8')
	for index := range successor.Roles {
		if successor.Roles[index].Bundle.Role != "coder" {
			continue
		}
		loaded := successor.Roles[index]
		loaded.Bundle.Version = "2.1.0"
		digest, digestErr := loaded.Bundle.ContentDigest()
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		loaded.Binding.BundleDigest = digest
		successor.Roles[index] = loaded
		successor.Manifest.Roles[index] = loaded.Binding
	}
	if err := resolver.handlers.Sync(successor); err != nil {
		t.Fatal(err)
	}

	oldGrounding, _, err := resolver.ResolveRoleHandlerGrounding(context.Background(), actor, initialBinding)
	if err != nil || oldGrounding.BundleVersion != "2.0.0" || oldGrounding.BundleDigest != initial.BundleDigest {
		t.Fatalf("in-flight grounding=%+v err=%v", oldGrounding, err)
	}
	current, err := resolver.handlers.Resolve(actor, "tekroo.message.task.assigned", "implementation")
	if err != nil || current.BundleVersion != "2.1.0" || current.BundleDigest == initial.BundleDigest {
		t.Fatalf("current dispatch=%+v err=%v", current, err)
	}
	currentBinding := handlerBindingFromDispatch(current, testUUIDValue(991), testDigestValue('7'))
	newGrounding, _, err := resolver.ResolveRoleHandlerGrounding(context.Background(), actor, currentBinding)
	if err != nil || newGrounding.BundleVersion != "2.1.0" || newGrounding.BundleDigest != current.BundleDigest {
		t.Fatalf("successor grounding=%+v err=%v", newGrounding, err)
	}
}

func handlerBindingFromDispatch(dispatch organization.RoleHandlerDispatch, messageID kernel.UUIDv7, bodyDigest kernel.Digest) kernel.HandlerDispatchBinding {
	return kernel.HandlerDispatchBinding{
		MessageID: messageID, MessageType: dispatch.MessageType, MessagePurpose: string(organization.PurposeHandoff), MessageBodyDigest: bodyDigest,
		SubscriptionPurpose: dispatch.SubscriptionPurpose, RoleBundleDigest: dispatch.BundleDigest,
		CharterDigest: dispatch.CharterDigest, HandlerDigest: dispatch.HandlerDigest,
		InputSchemaDigest: dispatch.InputSchemaDigest, ResultSchemaDigest: dispatch.ResultSchemaDigest,
		AllowedResults: append([]string(nil), dispatch.AllowedResults...), AllowedMessageProposals: append([]string(nil), dispatch.AllowedMessageProposals...),
	}
}

func loadStarterSuccessorTeam(t *testing.T) organization.LoadedTeam {
	t.Helper()
	manifestPath, err := filepath.Abs(filepath.Join("..", "..", "config", "starter-team", "team.v4.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifestHash := sha256.Sum256(manifestRaw)
	publicRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "message-handler-publisher.pub"))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicRaw)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("public key error=%v length=%d", err, len(publicKey))
	}
	qualificationRaw, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), "message-handler-publisher-qualification.pub"))
	if err != nil {
		t.Fatal(err)
	}
	qualificationKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(qualificationRaw)))
	if err != nil || len(qualificationKey) != ed25519.PublicKeySize {
		t.Fatalf("qualification key error=%v length=%d", err, len(qualificationKey))
	}
	team, err := organization.LoadTeamManifest(manifestPath, kernel.Digest(hex.EncodeToString(manifestHash[:])), map[string]ed25519.PublicKey{"tekroo-message-handlers-20260913": publicKey, "tekroo-message-handlers-qualification-20260913": qualificationKey})
	if err != nil {
		t.Fatal(err)
	}
	return team
}

func successorRoleState(t *testing.T, team organization.LoadedTeam, role string, instance uint32, executionID kernel.UUIDv7) organization.RoleInstanceState {
	t.Helper()
	for _, loaded := range team.Roles {
		if loaded.Binding.Role != role {
			continue
		}
		actor, err := kernel.ParseActorFQN(fmt.Sprintf("%s::%s-%d", team.Manifest.Team, role, instance))
		if err != nil {
			t.Fatal(err)
		}
		state := organization.RoleInstanceState{
			ActorFQN: actor, Team: team.Manifest.Team, Role: role, Instance: instance, Revision: 1,
			BundleDigest: loaded.Binding.BundleDigest, ModelProfile: loaded.Binding.ModelProfileDigest, WorkspaceID: loaded.Binding.WorkspaceIDs[0],
			Status: organization.RoleIdle, Execution: kernel.ExecutionTuple{ExecutionID: executionID, FencingEpoch: 1}, ProcessIdentity: "test",
			ManifestDigest: team.Digest, ManifestVersion: team.Manifest.Version, BundleVersion: loaded.Bundle.Version, RuntimeGeneration: 1,
		}
		if !state.Valid() {
			t.Fatalf("invalid role state: %#v", state)
		}
		return state
	}
	t.Fatalf("role %s not found", role)
	return organization.RoleInstanceState{}
}

func testUUIDValue(value int) kernel.UUIDv7 {
	return kernel.UUIDv7(fmt.Sprintf("00000000-0000-7000-8000-%012d", value))
}

func testDigestValue(value byte) kernel.Digest {
	return kernel.Digest(strings.Repeat(string(value), 64))
}
