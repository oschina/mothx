package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/oschina/mothx/internal/platform"
)

// JobsTool lists and manages background jobs.
type JobsTool struct {
	registry *Registry
	bashTool *BashTool
}

// NewJobsTool creates a new jobs tool.
func NewJobsTool(r *Registry, bashTool *BashTool) *JobsTool {
	return &JobsTool{registry: r, bashTool: bashTool}
}

func (t *JobsTool) Name() string { return "jobs" }

func (t *JobsTool) Description() string {
	return "List and check status of background jobs started with bash async=true. Shows running and finished jobs."
}

func (t *JobsTool) PromptSnippet() string {
	return "List and manage background jobs"
}

func (t *JobsTool) PromptGuidelines() []string {
	return nil
}

func (t *JobsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"jobId": {
				"type": "integer",
				"description": "Optional: get detailed status of a specific job by ID"
			},
			"cleanup": {
				"type": "boolean",
				"description": "Remove finished jobs from the list"
			}
		}
	}`)
}

func (t *JobsTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	jm := t.bashTool.GetJobManager()

	// Cleanup finished jobs
	if cleanup, _ := params["cleanup"].(bool); cleanup {
		for _, job := range jm.ListJobs() {
			if job.IsDone() {
				jm.RemoveJob(job.ID)
			}
		}
		return NewTextToolResult("Cleaned up finished jobs."), nil
	}

	// Get specific job
	if jobID, ok := params["jobId"].(float64); ok {
		job := jm.GetJob(int(jobID))
		if job == nil {
			return ToolResult{}, fmt.Errorf("job %d not found", int(jobID))
		}
		return NewTextToolResult(formatJobDetail(job)), nil
	}

	// List all jobs
	jobs := jm.ListJobs()
	if len(jobs) == 0 {
		return NewTextToolResult("No background jobs."), nil
	}

	// Sort by ID
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].ID < jobs[j].ID
	})

	var result string
	for _, job := range jobs {
		result += job.Status() + "\n"
	}
	return NewTextToolResult(result), nil
}

func formatJobDetail(job *BackgroundJob) string {
	job.mu.Lock()
	defer job.mu.Unlock()

	var result string
	result += fmt.Sprintf("Job ID:    %d\n", job.ID)
	result += fmt.Sprintf("Command:   %s\n", job.Command)
	result += fmt.Sprintf("PID:       %d\n", job.PID)
	result += fmt.Sprintf("Started:   %s\n", job.StartTime.Format("2006-01-02 15:04:05"))
	elapsed := time.Since(job.StartTime).Round(time.Second)
	result += fmt.Sprintf("Elapsed:   %s\n", elapsed)
	result += fmt.Sprintf("Status:    ")

	if job.done {
		if job.exitCode == 0 {
			result += "finished (success)\n"
		} else {
			result += fmt.Sprintf("finished (exit code %d)\n", job.exitCode)
		}

		if len(job.stdout) > 0 {
			result += fmt.Sprintf("STDOUT:\n%s\n", platform.DecodeProcessOutput(job.stdout))
		}
		if len(job.stderr) > 0 {
			result += fmt.Sprintf("STDERR:\n%s\n", platform.DecodeProcessOutput(job.stderr))
		}
		if job.err != nil {
			result += fmt.Sprintf("Error: %s\n", job.err)
		}
	} else {
		result += "running\n"
	}

	return result
}
