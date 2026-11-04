package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	acpclient "github.com/oschina/mothx/internal/acp/client"
	"github.com/oschina/mothx/internal/config"
)

const maxACPReadTextFileBytes = 1 << 20

const maxACPWriteTextFileBytes = 8 << 20

// acpReadTextFileAvailable reports whether this Runtime can satisfy the
// read-only client filesystem capability. Capability advertisement is decided
// once per pooled process, so a Runtime without a Registry must not claim it.
func (r *SessionRuntime) acpReadTextFileAvailable() bool {
	return r.acpReadTextFileAvailableIn(nil)
}

func (r *SessionRuntime) acpReadTextFileAvailableIn(workspace *acpWorkerWorkspace) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.closed && workspace.registryOr(r.Registry) != nil
}

// handleACPReadTextFile is the only client-owned filesystem method MothX
// advertises. It resolves paths through the same Registry roots as local read
// tools, then returns bounded UTF-8 text. It never writes and never follows a
// path outside the session workspace roots.
func (r *SessionRuntime) handleACPReadTextFile(ctx context.Context, message acpclient.Message, workspace *acpWorkerWorkspace) (any, *acpclient.RPCError) {
	request, ok := acpclient.DecodeReadTextFileRequest(message)
	if !ok {
		return nil, &acpclient.RPCError{Code: -32602, Message: "invalid fs/read_text_file request"}
	}
	if request.Line < 0 || request.Limit < 0 {
		return nil, &acpclient.RPCError{Code: -32602, Message: "fs/read_text_file line and limit must not be negative"}
	}
	if !filepath.IsAbs(filepath.FromSlash(request.Path)) {
		return nil, &acpclient.RPCError{Code: -32602, Message: "fs/read_text_file path must be absolute"}
	}
	r.mu.RLock()
	registry := workspace.registryOr(r.Registry)
	closed := r.closed
	r.mu.RUnlock()
	if closed || registry == nil {
		return nil, &acpclient.RPCError{Code: -32601, Message: "fs/read_text_file is not available"}
	}
	path, err := registry.ResolvePath(filepath.FromSlash(request.Path))
	if err != nil {
		return nil, &acpclient.RPCError{Code: -32001, Message: "path is outside the session workspace roots"}
	}
	content, err := readBoundedACPTextFile(ctx, path)
	if err != nil {
		return nil, &acpclient.RPCError{Code: -32001, Message: err.Error()}
	}
	return acpclient.ReadTextFileResult{Content: sliceACPTextLines(content, request.Line, request.Limit)}, nil
}

// acpWriteTextFileAvailable reports whether this child's contract may use the
// client-owned write bridge. The remote Agent still owns its own tools; this
// bridge only opts into a MothX-mediated write path when policy can enforce the
// task write scope, profile write roots, and shared workspace fence.
func (r *SessionRuntime) acpWriteTextFileAvailable(profile config.ACPAgentProfile, contract SubagentTaskContract) bool {
	return r.acpWriteTextFileAvailableIn(nil, profile, contract)
}

func (r *SessionRuntime) acpWriteTextFileAvailableIn(workspace *acpWorkerWorkspace, profile config.ACPAgentProfile, contract SubagentTaskContract) bool {
	if r == nil || !subagentRoleWrites(contract.Role) || len(profile.WriteRoots) == 0 {
		return false
	}
	if !profile.RoleAllows(config.ACPAgentRole(contract.Role)) {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return !r.closed && workspace.registryOr(r.Registry) != nil
}

// handleACPWriteTextFile is the only client-owned write method MothX
// advertises. It accepts only an existing write-role contract, resolves the
// path through the shared workspace roots/symlink fence, and requires the
// resolved path to stay inside both the task write scope and the profile's
// declared write roots.
func (r *SessionRuntime) handleACPWriteTextFile(ctx context.Context, message acpclient.Message, scope acpWorkerRequestScope) (any, *acpclient.RPCError) {
	request, ok := acpclient.DecodeWriteTextFileRequest(message)
	if !ok {
		return nil, &acpclient.RPCError{Code: -32602, Message: "invalid fs/write_text_file request"}
	}
	if len(request.Content) > maxACPWriteTextFileBytes {
		return nil, &acpclient.RPCError{Code: -32001, Message: "fs/write_text_file content exceeds the Runtime limit"}
	}
	if !filepath.IsAbs(filepath.FromSlash(request.Path)) {
		return nil, &acpclient.RPCError{Code: -32602, Message: "fs/write_text_file path must be absolute"}
	}
	if !r.acpWriteTextFileAvailableIn(scope.Workspace, scope.Profile, scope.Contract) {
		return nil, &acpclient.RPCError{Code: -32601, Message: "fs/write_text_file is not available for this task"}
	}
	r.mu.RLock()
	registry := scope.Workspace.registryOr(r.Registry)
	closed := r.closed
	r.mu.RUnlock()
	if closed || registry == nil {
		return nil, &acpclient.RPCError{Code: -32601, Message: "fs/write_text_file is not available"}
	}
	resolved, err := registry.ResolvePath(filepath.FromSlash(request.Path))
	if err != nil {
		return nil, &acpclient.RPCError{Code: -32001, Message: "path is outside the session workspace roots"}
	}
	if !acpWritePathAuthorized(resolved, scope.WorkDir, scope.Contract.WriteScope, scope.Profile.WriteRoots) {
		return nil, &acpclient.RPCError{Code: -32001, Message: "path is outside the task write scope"}
	}
	if info, statErr := os.Stat(resolved); statErr == nil && info.IsDir() {
		return nil, &acpclient.RPCError{Code: -32001, Message: "requested path is a directory"}
	}
	if _, err := registry.WriteFile(ctx, resolved, request.Content, "acp:"+scope.ChildID); err != nil {
		return nil, &acpclient.RPCError{Code: -32001, Message: fmt.Sprintf("write text file: %v", err)}
	}
	return nil, nil
}

// acpWritePathAuthorized compares the symlink-resolved target with the
// symlink-resolved work directory before checking the task/profile scopes.
// Checking the lexical path alone would let an in-workspace symlink redirect a
// write outside the declared scope.
func acpWritePathAuthorized(resolvedPath, workDir string, writeScope, writeRoots []string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(workDir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return subagentWriteWithinRoots(rel, writeScope) && subagentWriteWithinRoots(rel, writeRoots)
}

func readBoundedACPTextFile(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read text file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat text file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("requested path is not a regular file")
	}
	if info.Size() > maxACPReadTextFileBytes {
		return "", fmt.Errorf("requested file exceeds %d bytes", maxACPReadTextFileBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxACPReadTextFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read text file: %w", err)
	}
	if len(data) > maxACPReadTextFileBytes {
		return "", fmt.Errorf("requested file exceeds %d bytes", maxACPReadTextFileBytes)
	}
	if !utf8.Valid(data) {
		return "", errors.New("requested file is not valid UTF-8 text")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	return string(data), nil
}

// sliceACPTextLines applies ACP's optional 1-based starting line and maximum
// line count while preserving the original line endings.
func sliceACPTextLines(content string, line, limit int) string {
	if line <= 1 && limit == 0 {
		return content
	}
	start := 0
	if line > 1 {
		current := 1
		for current < line {
			offset := strings.IndexByte(content[start:], '\n')
			if offset < 0 {
				return ""
			}
			start += offset + 1
			current++
		}
	}
	end := len(content)
	if limit > 0 {
		position, lines := start, 0
		for lines < limit && position < len(content) {
			offset := strings.IndexByte(content[position:], '\n')
			if offset < 0 {
				position = len(content)
				break
			}
			position += offset + 1
			lines++
		}
		end = position
	}
	if start >= end {
		return ""
	}
	return content[start:end]
}
