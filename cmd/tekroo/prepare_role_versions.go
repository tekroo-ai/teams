package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/tekroo-ai/teams/adapters/operationalruntime"
	"github.com/tekroo-ai/teams/kernel"
)

func runRoleVersionPreparation(arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("tekroo prepare-role-versions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "absolute qualified template configuration")
	policyPath := flags.String("version-policy", "", "absolute centrally configured role range/catalog policy")
	output := flags.String("output", "", "absolute new directory for frozen signed packages/configuration/lock")
	runID := flags.String("run-id", "", "new run identity")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}
	prepared, lock, err := operationalruntime.PrepareRoleVersionRunFromFile(*configPath, *policyPath, *output, *runID, time.Now().UTC())
	if err != nil {
		return err
	}
	// Do not expose model credentials or full profiles on stdout. The private
	// prepared configuration is consumed by the normal run preparation path.
	receipt := struct {
		RunID        string                             `json:"run_id"`
		ConfigFile   string                             `json:"config_file"`
		ManifestFile string                             `json:"manifest_file"`
		LockFile     string                             `json:"lock_file"`
		LockDigest   kernel.Digest                      `json:"lock_digest"`
		PolicyDigest kernel.Digest                      `json:"policy_digest"`
		Roles        []operationalruntime.LockedRunRole `json:"roles"`
	}{lock.RunID, filepath.Join(*output, "prepared-config.json"), prepared.Organization.ManifestFile, prepared.Organization.RoleLockFile, prepared.Organization.RoleLockDigest, lock.PolicyDigest, lock.Roles}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", raw)
	return err
}
