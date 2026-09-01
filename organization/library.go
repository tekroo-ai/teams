package organization

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tekroo-ai/teams/kernel"
)

var ErrRoleLibraryConflict = errors.New("role library version or content conflict")

type RoleLibraryEntry struct {
	Team           string        `json:"team"`
	Version        string        `json:"version"`
	ManifestDigest kernel.Digest `json:"manifest_digest"`
	Roles          []string      `json:"roles"`
}

// RoleLibrary is an in-process index over already authenticated, content-bound
// team manifests. Signature and digest verification remains the loader's job;
// this index enforces deterministic, monotonic synchronization.
type RoleLibrary struct {
	mu      sync.RWMutex
	entries map[string]RoleLibraryEntry
}

func NewRoleLibrary(teams ...LoadedTeam) (*RoleLibrary, error) {
	library := &RoleLibrary{entries: make(map[string]RoleLibraryEntry, len(teams))}
	for _, team := range teams {
		if _, err := library.Sync(team); err != nil {
			return nil, err
		}
	}
	return library, nil
}

// Sync accepts a new team, accepts an exact idempotent repeat, and rejects a
// rollback or a different manifest at the already-published version.
func (library *RoleLibrary) Sync(team LoadedTeam) (RoleLibraryEntry, error) {
	if library == nil || team.Manifest.Validate() != nil || !team.Digest.Valid() || len(team.Roles) != len(team.Manifest.Roles) {
		return RoleLibraryEntry{}, ErrInvalidTeamManifest
	}
	entry := RoleLibraryEntry{Team: team.Manifest.Team, Version: team.Manifest.Version, ManifestDigest: team.Digest, Roles: make([]string, len(team.Roles))}
	for index, role := range team.Roles {
		if role.Binding.Role != role.Bundle.Role || role.Bundle.Validate() != nil {
			return RoleLibraryEntry{}, ErrInvalidTeamManifest
		}
		entry.Roles[index] = role.Binding.Role
	}
	if !sort.StringsAreSorted(entry.Roles) {
		return RoleLibraryEntry{}, ErrInvalidTeamManifest
	}
	library.mu.Lock()
	defer library.mu.Unlock()
	current, found := library.entries[entry.Team]
	if found {
		comparison := compareSemanticVersions(entry.Version, current.Version)
		if comparison < 0 || comparison == 0 && entry.ManifestDigest != current.ManifestDigest {
			return RoleLibraryEntry{}, ErrRoleLibraryConflict
		}
		if comparison == 0 {
			return cloneLibraryEntry(current), nil
		}
	}
	library.entries[entry.Team] = cloneLibraryEntry(entry)
	return cloneLibraryEntry(entry), nil
}

func (library *RoleLibrary) List() []RoleLibraryEntry {
	if library == nil {
		return nil
	}
	library.mu.RLock()
	defer library.mu.RUnlock()
	result := make([]RoleLibraryEntry, 0, len(library.entries))
	for _, entry := range library.entries {
		result = append(result, cloneLibraryEntry(entry))
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Team < result[right].Team })
	return result
}

func cloneLibraryEntry(entry RoleLibraryEntry) RoleLibraryEntry {
	entry.Roles = append([]string(nil), entry.Roles...)
	return entry
}

func compareSemanticVersions(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := range 3 {
		leftValue, _ := strconv.ParseUint(leftParts[index], 10, 64)
		rightValue, _ := strconv.ParseUint(rightParts[index], 10, 64)
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}
