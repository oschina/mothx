package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentpkg "github.com/oschina/mothx/agent"
	"github.com/oschina/mothx/internal/agent"
	"github.com/oschina/mothx/internal/session"
	worktreepkg "github.com/oschina/mothx/internal/worktree"
)

// Integration evidence statuses. They describe one Runtime-owned application
// attempt, never a worker's claim about its own worktree.
const (
	ChangeSetIntegrationApplied      = "applied"
	ChangeSetIntegrationConflict     = "conflict"
	ChangeSetIntegrationRejected     = "rejected"
	ChangeSetIntegrationFailed       = "failed"
	ChangeSetIntegrationNotAttempted = "not_attempted"
)

const maxRuntimeChangeSetSelections = 8

// RuntimeChangeSetIntegrator is the single owner of applying immutable child
// change-set snapshots to the Router session workspace. It reads only durable
// child evidence and snapshot bytes; it never copies a mutable child worktree.
type RuntimeChangeSetIntegrator struct {
	Runtime   *SessionRuntime
	Worktrees *WorktreeManager
}

type loadedChangeSet struct {
	selection agent.ChangeSetSelection
	childID   string
	record    *session.ChildRun
	changeSet worktreepkg.ChangeSet
	applied   bool
}

// IntegrateChangeSets validates every selection first, acquires the exclusive
// base-workspace lane, then applies snapshots in request order. A conflict is
// returned as structured evidence with a nil error so the lead can inspect and
// recover; setup failures and unsafe snapshots return an error.
func (i *RuntimeChangeSetIntegrator) IntegrateChangeSets(ctx context.Context, selections []agent.ChangeSetSelection) (agent.ChangeSetIntegrationResult, error) {
	if i == nil || i.Runtime == nil {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration requires an attached Runtime")
	}
	if i.Worktrees == nil {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration requires the Runtime worktree manager")
	}
	if err := i.Runtime.ensureOpen(); err != nil {
		return agent.ChangeSetIntegrationResult{}, err
	}
	_, _, _, mode, _ := i.Runtime.ConfigSnapshot()
	if mode != ModeRouter {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration is only available in Router mode")
	}
	if len(selections) == 0 {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("at least one change-set selection is required")
	}
	if len(selections) > maxRuntimeChangeSetSelections {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration exceeds %d selections", maxRuntimeChangeSetSelections)
	}
	i.Runtime.mu.RLock()
	manager := i.Runtime.Manager
	workDir := filepath.Clean(strings.TrimSpace(i.Runtime.WorkDir))
	i.Runtime.mu.RUnlock()
	if manager == nil {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration requires a session manager")
	}
	if workDir == "" || workDir == "." || !filepath.IsAbs(workDir) {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration requires an absolute session work directory")
	}
	sessionID := i.Runtime.sessionIDForDecisions()
	sessionDir := manager.GetSessionDir()
	if sessionID == "" || sessionDir == "" {
		return agent.ChangeSetIntegrationResult{}, fmt.Errorf("change-set integration requires a persisted session")
	}

	result := agent.ChangeSetIntegrationResult{
		IntegrationID: session.GenerateID(),
		Status:        ChangeSetIntegrationApplied,
		Children:      make([]agent.ChangeSetIntegrationChild, 0, len(selections)),
	}
	loaded := make([]loadedChangeSet, 0, len(selections))
	seen := make(map[string]struct{}, len(selections))
	for _, selection := range selections {
		handle := strings.TrimSpace(selection.Handle)
		digest := strings.TrimSpace(selection.Digest)
		childResult := agent.ChangeSetIntegrationChild{Handle: handle, Digest: digest, Status: ChangeSetIntegrationRejected}
		if handle == "" || digest == "" {
			childResult.Error = "handle and digest are required"
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if _, duplicate := seen[handle]; duplicate {
			childResult.Error = "child handle is selected more than once"
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		seen[handle] = struct{}{}
		record, found, err := session.LoadChildRun(sessionDir, sessionID, handle)
		if err != nil {
			return result, fmt.Errorf("load child %q change set: %w", handle, err)
		}
		if !found || record == nil {
			childResult.Error = "durable child history was not found"
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if record.Status != string(agentpkg.TaskSuccess) {
			childResult.Error = fmt.Sprintf("child status %q is not successful", record.Status)
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if strings.TrimSpace(record.ChangeSetError) != "" {
			childResult.Error = "change-set capture failed: " + record.ChangeSetError
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if strings.TrimSpace(record.ChangeSetJSON) == "" {
			childResult.Error = "child has no change-set snapshot"
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		var changeSet worktreepkg.ChangeSet
		if err := json.Unmarshal([]byte(record.ChangeSetJSON), &changeSet); err != nil {
			childResult.Error = "decode change-set snapshot: " + err.Error()
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if err := changeSet.Verify(); err != nil {
			childResult.Error = err.Error()
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if changeSet.Digest != digest {
			childResult.Error = fmt.Sprintf("digest %q does not match snapshot %q", digest, changeSet.Digest)
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if changeSet.PatchTruncated || changeSet.UntrackedTruncated {
			childResult.Error = "a truncated change set cannot be integrated"
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		if strings.TrimSpace(record.WorktreeID) == "" {
			childResult.Error = "child history has no managed worktree identity"
			result.Status = ChangeSetIntegrationFailed
			result.Children = append(result.Children, childResult)
			continue
		}
		childResult.Status = ChangeSetIntegrationNotAttempted
		result.Children = append(result.Children, childResult)
		loaded = append(loaded, loadedChangeSet{
			selection: agent.ChangeSetSelection{Handle: handle, Digest: digest},
			childID:   handle,
			record:    record,
			changeSet: changeSet,
			applied:   record.IntegrationStatus == ChangeSetIntegrationApplied && record.IntegrationDigest == digest,
		})
	}
	if result.Status != ChangeSetIntegrationApplied {
		result.Error = "one or more selected change sets failed validation"
		return result, nil
	}

	release, err := i.Runtime.acquireACPWorkerPermitForRole(1, SubagentRoleIntegrate)
	if err != nil {
		return result, fmt.Errorf("acquire integration lane: %w", err)
	}
	defer release()

	// The validation snapshot above is intentionally lock-free, so another
	// Runtime may have applied the same selection while this request waited for
	// the exclusive lane. Re-read the durable evidence now that the lane is
	// held; applying an already-applied snapshot again would be a conflict.
	for index := range loaded {
		item := &loaded[index]
		current, found, loadErr := session.LoadChildRun(sessionDir, sessionID, item.childID)
		if loadErr != nil {
			return result, fmt.Errorf("reload child %q integration evidence: %w", item.childID, loadErr)
		}
		if found && current != nil &&
			current.IntegrationStatus == ChangeSetIntegrationApplied &&
			current.IntegrationDigest == item.selection.Digest {
			item.applied = true
		}
	}

	appliedFiles := make([]string, 0)
	type appliedInRequest struct {
		index   int
		item    *loadedChangeSet
		applied worktreepkg.ApplyResult
	}
	appliedThisRequest := make([]appliedInRequest, 0, len(loaded))
	for index := range loaded {
		item := &loaded[index]
		childResult := &result.Children[index]
		if item.applied {
			childResult.Status = ChangeSetIntegrationApplied
			childResult.AppliedFiles = append([]string(nil), changeSetPaths(item.changeSet)...)
			appliedFiles = append(appliedFiles, childResult.AppliedFiles...)
			continue
		}
		applied, applyErr := worktreepkg.ApplyChangeSet(ctx, workDir, item.changeSet)
		if applyErr != nil {
			// A multi-selection request is transactional within this Runtime:
			// roll back the partial current apply (if any) and every earlier
			// selection this request added. Selections that were already
			// applied before this request are not touched.
			if applied.PatchApplied || len(applied.CreatedUntracked) > 0 {
				if rollbackErr := worktreepkg.RollbackChangeSet(ctx, workDir, item.changeSet, applied); rollbackErr != nil {
					applyErr = fmt.Errorf("%w; rollback of the partial selection failed: %v", applyErr, rollbackErr)
				}
			}
			now := time.Now().UTC()
			for previousIndex := len(appliedThisRequest) - 1; previousIndex >= 0; previousIndex-- {
				previous := appliedThisRequest[previousIndex]
				previousChild := &result.Children[previous.index]
				previousChild.Status = ChangeSetIntegrationFailed
				previousChild.AppliedFiles = nil
				if rollbackErr := worktreepkg.RollbackChangeSet(ctx, workDir, previous.item.changeSet, previous.applied); rollbackErr != nil {
					previousChild.Error = fmt.Sprintf("rolled back after a later integration failure: %v", rollbackErr)
				} else {
					previousChild.Error = "rolled back because a later selected change set could not be applied"
				}
				recordIntegrationEvidence(&result, previousChild, sessionDir, sessionID, previous.item.childID, previous.item.selection.Digest, now)
			}
			childResult.Status = ChangeSetIntegrationFailed
			var typed *worktreepkg.Error
			if errors.As(applyErr, &typed) && typed.Code == worktreepkg.CodeApplyConflict {
				childResult.Status = ChangeSetIntegrationConflict
			}
			childResult.Error = applyErr.Error()
			result.Status = ChangeSetIntegrationFailed
			result.Error = applyErr.Error()
			recordIntegrationEvidence(&result, childResult, sessionDir, sessionID, item.childID, item.selection.Digest, now)
			for rest := index + 1; rest < len(loaded); rest++ {
				// A child whose snapshot already landed before this request keeps
				// its durable "applied" evidence. The idempotency check reads
				// that record, so downgrading it to "not_attempted" would make
				// the next request re-apply (and conflict on) a snapshot that
				// is already in the workspace.
				if loaded[rest].applied {
					result.Children[rest].Status = ChangeSetIntegrationApplied
					result.Children[rest].Error = ""
					result.Children[rest].AppliedFiles = append([]string(nil), changeSetPaths(loaded[rest].changeSet)...)
					continue
				}
				result.Children[rest].Status = ChangeSetIntegrationNotAttempted
				result.Children[rest].Error = "not attempted after an earlier integration failure"
				recordIntegrationEvidence(&result, &result.Children[rest], sessionDir, sessionID, loaded[rest].childID, loaded[rest].selection.Digest, now)
			}
			return result, nil
		}
		childResult.Status = ChangeSetIntegrationApplied
		childResult.AppliedFiles = append([]string(nil), applied.AppliedFiles...)
		appliedFiles = append(appliedFiles, applied.AppliedFiles...)
		recordIntegrationEvidence(&result, childResult, sessionDir, sessionID, item.childID, item.selection.Digest, time.Now().UTC())
		appliedThisRequest = append(appliedThisRequest, appliedInRequest{index: index, item: item, applied: applied})
	}
	result.Status = ChangeSetIntegrationApplied
	result.AppliedFiles = dedupePreservingOrder(appliedFiles)
	return result, nil
}

func changeSetPaths(changeSet worktreepkg.ChangeSet) []string {
	paths := make([]string, 0, len(changeSet.Files)+len(changeSet.UntrackedFiles))
	for _, file := range changeSet.Files {
		if file.Status == "untracked" {
			continue
		}
		paths = append(paths, file.Path)
	}
	for _, file := range changeSet.UntrackedFiles {
		paths = append(paths, file.Path)
	}
	return paths
}

func dedupePreservingOrder(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func recordIntegrationEvidence(result *agent.ChangeSetIntegrationResult, child *agent.ChangeSetIntegrationChild, sessionDir, sessionID, childID, digest string, at time.Time) {
	err := session.MarkChildRunIntegration(
		sessionDir, sessionID, []string{childID}, result.IntegrationID,
		digest, child.Status, child.Error, at,
	)
	if err == nil {
		return
	}
	message := "record integration evidence: " + err.Error()
	child.EvidenceError = message
	if result.EvidenceError == "" {
		result.EvidenceError = message
	}
}
