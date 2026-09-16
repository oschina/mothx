package agentruntime

import (
	"context"
	"fmt"
	"strings"
)

// KnowledgeBaseCronJobPrefix namespaces scheduled reindex jobs inside the
// shared cron store. The store is keyed only by sessionDir, so every scheduler
// process (ACP, serve) can claim a namespaced job; each must route it through
// RunKnowledgeBaseCronJob instead of executing the job prompt as an ordinary
// agent run inside the knowledge source directory.
const KnowledgeBaseCronJobPrefix = "knowledge-base-index:"

// KnowledgeBaseCronJobID derives the shared cron identity of one knowledge
// base's reindex schedule.
func KnowledgeBaseCronJobID(knowledgeBaseID string) string {
	return KnowledgeBaseCronJobPrefix + strings.TrimSpace(knowledgeBaseID)
}

// KnowledgeBaseIDFromCronJobID extracts the knowledge base identity from a
// namespaced cron job ID.
func KnowledgeBaseIDFromCronJobID(jobID string) (string, bool) {
	id := strings.TrimSpace(strings.TrimPrefix(jobID, KnowledgeBaseCronJobPrefix))
	return id, strings.HasPrefix(jobID, KnowledgeBaseCronJobPrefix) && id != ""
}

// RunKnowledgeBaseCronJob wakes one namespaced reindex through the same
// background job machinery as manual scans, so Cron only records the
// scheduling outcome while the canonical durable Run stays Runtime-owned.
// It reports handled=false for job IDs outside the knowledge namespace so a
// shared scheduler falls back to its own execution path. Passing the
// process-wide cached service keeps scheduled scans deduplicated against
// manual scans and visible to progress polling.
func RunKnowledgeBaseCronJob(ctx context.Context, service *KnowledgeBaseService, jobID string) (bool, string, error) {
	id, ok := KnowledgeBaseIDFromCronJobID(jobID)
	if !ok {
		return false, "", nil
	}
	if service == nil {
		return true, "", errKnowledgeBaseServiceNil
	}
	job, err := service.StartIndex(ctx, id, SourceCron)
	if err != nil {
		return true, "", err
	}
	snapshot, err := job.Wait(ctx)
	if err != nil {
		return true, "", err
	}
	return true, fmt.Sprintf("indexed knowledge base %s: %d files, %d chunks", id, snapshot.FileCount, snapshot.ChunkCount), nil
}
