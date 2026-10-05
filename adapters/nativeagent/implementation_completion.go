package nativeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tekroo-ai/teams/agenttools"
	"github.com/tekroo-ai/teams/application"
	"github.com/tekroo-ai/teams/kernel"
)

func implementationCompletionCheck(brief application.ExecutionBrief, digest kernel.Digest, gateway agenttools.Gateway, ledger agenttools.EffectLedger, previous func(context.Context, []byte) error) func(context.Context, []byte) error {
	return func(ctx context.Context, output []byte) error {
		if previous != nil {
			if err := previous(ctx, output); err != nil {
				return err
			}
		}
		raw := strings.TrimPrefix(string(output), application.OrganizationalResultMarker)
		var result struct {
			Outcome string `json:"outcome"`
		}
		if json.Unmarshal([]byte(raw), &result) != nil {
			return ErrInvalidBinding
		}
		// A genuine blocker remains reportable without a candidate or a passing build.
		if result.Outcome != "completed" {
			return nil
		}
		status := func() (agenttools.Result, error) {
			r, err := gateway.ExecuteReadOnly(ctx, agenttools.Request{InvocationID: brief.InvocationID, RequestDigest: digest, ToolCallID: "completion-status", Call: agenttools.Call{Name: "git_status", Arguments: json.RawMessage(`{}`)}})
			return r.Result, err
		}
		before, err := status()
		if err != nil {
			return err
		}
		if before.Clean == nil || !*before.Clean || before.HEAD == brief.Scope.BaselineSHA || before.HEAD == "" || brief.Scope.Branch != "" && before.Branch != brief.Scope.Branch {
			return fmt.Errorf("implementation handoff requires a clean successor commit on the assigned branch; current HEAD=%s, baseline=%s, status=%s", before.HEAD, brief.Scope.BaselineSHA, before.Output)
		}
		required := false
		for _, gate := range brief.WorkProfile.RequiredDeterministicGateIDs {
			if gate == "go-test" {
				required = true
			}
		}
		if required {
			checked, err := (agenttools.BuildGateway{Bindings: gateway.Bindings, Host: gateway.Host, Ledger: ledger, Policy: brief.EffectPolicyDigest}).Verify(ctx, brief.InvocationID, digest, before.HEAD)
			if err != nil {
				return fmt.Errorf("implementation build check unavailable: %w", err)
			}
			if checked.ExecutionError != "" || checked.ExitCode != 0 {
				return fmt.Errorf("committed candidate %s failed the build handoff check; remain on this task and resolve the compiler errors before reporting completed:\n%s\n%s", before.HEAD, checked.ExecutionError, checked.Output)
			}
		}
		after, err := status()
		if err != nil {
			return err
		}
		if after.Clean == nil || !*after.Clean || after.HEAD != before.HEAD || after.Branch != before.Branch {
			return fmt.Errorf("candidate changed during implementation handoff check")
		}
		return nil
	}
}
