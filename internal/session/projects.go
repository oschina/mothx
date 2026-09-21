package session

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/startvibecoding/mothx/internal/dao"
	"strings"
	"time"
)

type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type SessionMetadata struct {
	ProjectID string `json:"projectId,omitempty"`
	Pinned    bool   `json:"pinned"`
	// UpdatedAt is the persisted revision time of the metadata row. It is
	// read-only output; SetSessionMetadata always stamps the write time itself.
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

func parseProjectTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func ListProjects(sessionDir string) ([]Project, error) {
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return []Project{}, err
	}
	records, err := dao.NewProjectDAO(db.Bun()).List(context.Background())
	if err != nil {
		return nil, err
	}
	projects := []Project{}
	for _, record := range records {
		p := Project{ID: record.ID, Name: record.Name,
			CreatedAt: parseProjectTime(record.CreatedAt), UpdatedAt: parseProjectTime(record.UpdatedAt)}
		projects = append(projects, p)
	}
	return projects, nil
}

func CreateProject(sessionDir, name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, fmt.Errorf("project name is required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return Project{}, err
	}
	now := time.Now().UTC()
	id := GenerateID()
	err = dao.NewProjectDAO(db.Bun()).Insert(context.Background(), &dao.ProjectRecord{ID: id, Name: name, CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano)})
	if err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}, nil
}

func RenameProject(sessionDir, id, name string) (Project, error) {
	id, name = strings.TrimSpace(id), strings.TrimSpace(name)
	if id == "" || name == "" {
		return Project{}, fmt.Errorf("project ID and name are required")
	}
	db, err := OpenRootDB(sessionDir)
	if err != nil {
		return Project{}, err
	}
	now := time.Now().UTC()
	changed, err := dao.NewProjectDAO(db.Bun()).UpdateName(context.Background(), id, name, now.Format(time.RFC3339Nano))
	if err != nil {
		return Project{}, err
	}
	if changed == 0 {
		return Project{}, fmt.Errorf("project not found")
	}
	return Project{ID: id, Name: name, UpdatedAt: now}, nil
}

func DeleteProject(sessionDir, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("project ID is required")
	}
	// Realize the declared ON DELETE SET NULL reference semantics explicitly so
	// session assignments never outlive their project, regardless of SQLite
	// foreign-key enforcement. Both writes share one transaction so a failed
	// delete cannot commit only the detach half.
	return WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		return dao.NewProjectDAO(nil).DeleteWithMetadata(context.Background(), tx, id)
	})
}

func SetSessionMetadata(sessionDir, sessionID string, metadata SessionMetadata) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session ID is required")
	}
	metadata.ProjectID = strings.TrimSpace(metadata.ProjectID)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var projectID *string
	if metadata.ProjectID != "" {
		projectID = &metadata.ProjectID
	}
	record := &dao.SessionMetadataRecord{SessionID: sessionID, ProjectID: projectID, Pinned: boolToInt(metadata.Pinned), UpdatedAt: now}
	return WriteRootDatabase(context.Background(), sessionDir, func(tx *dao.Tx) error {
		changed, err := dao.NewProjectDAO(nil).UpsertMetadataIfReferencesExist(context.Background(), tx, record)
		if err != nil {
			return err
		}
		if changed == 1 {
			return nil
		}
		if metadata.ProjectID != "" {
			exists, err := dao.NewProjectDAO(nil).ExistsWith(context.Background(), tx, metadata.ProjectID)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("project not found")
			}
		}
		return fmt.Errorf("session not found")
	})
}

func LatestSessionTitle(sessionDir, sessionID string) (string, string, error) {
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return "", "", err
	}
	data, err := dao.NewProjectDAO(db.Bun()).LatestSessionInfoData(context.Background(), sessionID)
	if err == dao.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var entry SessionInfoEntry
	if err := json.Unmarshal([]byte(data), &entry); err != nil {
		return "", "", err
	}
	return entry.Name, entry.Source, nil
}

func GetSessionMetadata(sessionDir, sessionID string) (SessionMetadata, error) {
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return SessionMetadata{}, err
	}
	record, err := dao.NewProjectDAO(db.Bun()).Metadata(context.Background(), sessionID)
	if err != nil {
		return SessionMetadata{}, err
	}
	if record == nil {
		return SessionMetadata{}, nil
	}
	var metadata SessionMetadata
	if record.ProjectID != nil {
		metadata.ProjectID = *record.ProjectID
	}
	metadata.Pinned = record.Pinned != 0
	metadata.UpdatedAt = parseProjectTime(record.UpdatedAt)
	return metadata, nil
}

// ListSessionMetadata returns the persisted project/pin metadata of the given
// sessions in one read-only query, keyed by session ID. Sessions without a
// metadata row are absent from the result. All SQL stays in the DAO.
func ListSessionMetadata(sessionDir string, sessionIDs []string) (map[string]SessionMetadata, error) {
	result := make(map[string]SessionMetadata)
	if len(sessionIDs) == 0 {
		return result, nil
	}
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return result, err
	}
	records, err := dao.NewProjectDAO(db.Bun()).MetadataForSessions(context.Background(), sessionIDs)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		metadata := SessionMetadata{Pinned: record.Pinned != 0, UpdatedAt: parseProjectTime(record.UpdatedAt)}
		if record.ProjectID != nil {
			metadata.ProjectID = *record.ProjectID
		}
		result[record.SessionID] = metadata
	}
	return result, nil
}

// ProjectSessionCounts returns how many sessions are currently assigned to
// each project. It is a read-only projection for project listings.
func ProjectSessionCounts(sessionDir string) (map[string]int, error) {
	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return map[string]int{}, err
	}
	return dao.NewProjectDAO(db.Bun()).SessionCountsByProject(context.Background())
}
