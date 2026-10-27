package session

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/oschina/mothx/internal/dao"
	"github.com/oschina/mothx/internal/platform"
	"github.com/oschina/mothx/internal/provider"
	"github.com/oschina/mothx/internal/util"
)

// sessionSearchProjection is the one read-only EventStore observation a search
// needs: every searchable text, the latest display title, and the first user
// message preview. Matching and detail filling both use this single value, so a
// searched EventStore session is never replayed twice (and never through the
// display cache, which would require another open path).
type sessionSearchProjection struct {
	title   string
	preview string
	texts   []string
}

// sessionSearchScan is the shared result of one search pass. total and page are
// produced by the same candidate scan, which is what lets a paginated caller
// trust the pair without a second COUNT pass.
type sessionSearchScan struct {
	total              int
	page               []dao.SessionRecord
	aggregates         dao.SessionDetailAggregates
	eventStoreProjects map[string]sessionSearchProjection
	legacyPreviews     map[string]string
}

// SearchAllDetailed searches sessions by one case-insensitive substring and
// returns both the requested page and the total number of matches. ID, working
// directory, channel metadata, session title, preview, and persisted message
// text (including compaction summaries and content overrides) are matched.
//
// The pass is read-only and single-shot: candidates, aggregates, legacy payload
// rows, and EventStore projections are each read once. No Manager is opened,
// no lease is acquired, no write transaction is taken, and EventStore
// convergence is never triggered.
func SearchAllDetailed(sessionDir, query string, limit, offset int) ([]SessionDetail, int, error) {
	return searchAllDetailed(sessionDir, query, limit, offset, dao.SessionListFilter{MessagesOnly: true})
}

// searchAllDetailed is the option-aware form behind SearchAllDetailed and the
// legacy ListAll/ListAllDetailed WithSearch entry points. messagesOnly keeps
// their historical candidate filter without introducing a second matcher.
func searchAllDetailed(sessionDir, query string, limit, offset int, filter dao.SessionListFilter) ([]SessionDetail, int, error) {
	scan, err := scanSessionSearch(sessionDir, query, limit, offset, filter)
	if err != nil {
		return nil, 0, err
	}
	if len(scan.page) == 0 {
		return nil, scan.total, nil
	}
	return buildSearchDetails(sessionDir, scan), scan.total, nil
}

// scanSessionSearch performs the single candidate + content pass. Cheap fields
// (ID/cwd/channel/title/preview) are evaluated from the list row and the
// aggregate projection; only sessions that miss them read persisted content.
func scanSessionSearch(sessionDir, query string, limit, offset int, filter dao.SessionListFilter) (sessionSearchScan, error) {
	result := sessionSearchScan{aggregates: newSessionDetailAggregates(), eventStoreProjects: map[string]sessionSearchProjection{}, legacyPreviews: map[string]string{}}
	if sessionDir == "" {
		sessionDir = platform.SessionDir()
	}
	needle := strings.ToLower(query)

	db, ok, err := openExistingSessionDB(sessionDir)
	if err != nil || !ok {
		return result, err
	}
	sessionDAO := dao.NewSessionDAO(db.Bun())
	// Search performs its own matching and pagination over the complete candidate
	// set, so only the SQL-supplied scope predicates may remain here.
	filter.Limit, filter.Offset, filter.Search = 0, 0, ""
	records, err := sessionDAO.List(context.Background(), filter)
	if err != nil {
		return result, err
	}
	if len(records) == 0 {
		return result, nil
	}

	ids := make([]string, len(records))
	recordIndex := make(map[string]int, len(records))
	for i := range records {
		ids[i] = records[i].ID
		recordIndex[records[i].ID] = i
	}
	aggregates, err := sessionDAO.DetailAggregates(context.Background(), ids)
	if err != nil {
		return result, err
	}
	result.aggregates = aggregates

	matched := make([]bool, len(records))
	legacyIDs := make([]string, 0, len(records))
	eventStoreIDs := make([]string, 0, len(records))
	for i := range records {
		if sessionRecordCheapMatch(records[i], aggregates, needle) {
			matched[i] = true
			continue
		}
		if records[i].ContentStore == "event_store" {
			eventStoreIDs = append(eventStoreIDs, records[i].ID)
			continue
		}
		legacyIDs = append(legacyIDs, records[i].ID)
	}

	// One ordered, streaming query decodes the remaining legacy payload rows
	// one session at a time. Replay applies content overrides and compaction
	// before matching, exactly like GetMessages(), so text that is no longer part
	// of the effective transcript cannot produce a hit.
	if len(legacyIDs) > 0 {
		contentMatched := make(map[string]struct{}, len(legacyIDs))
		currentID := ""
		var currentEntries []interface{}
		flush := func() {
			if currentID == "" {
				return
			}
			if len(currentEntries) > 0 {
				state := buildReplayState(currentEntries)
				if replayMessagesMatch(state.messages, needle) {
					contentMatched[currentID] = struct{}{}
					result.legacyPreviews[currentID] = replayPreview(state.messages)
				}
			}
			currentEntries = nil
		}
		err := sessionDAO.StreamSearchContentEntries(context.Background(), legacyIDs, func(row dao.SessionSearchEntryRecord) error {
			if row.SessionID != currentID {
				flush()
				currentID = row.SessionID
			}
			if _, done := contentMatched[currentID]; done {
				return nil
			}
			if entry, ok := decodeLegacySearchEntry(row.Type, row.Data); ok {
				currentEntries = append(currentEntries, entry)
			}
			return nil
		})
		if err != nil {
			return result, err
		}
		flush()
		for i := range records {
			if matched[i] {
				continue
			}
			if _, ok := contentMatched[records[i].ID]; ok {
				matched[i] = true
			}
		}
	}

	// EventStore sessions are content-only for search purposes: a healthy store
	// is read once (never converged or repaired) and its projection is reused for
	// detail filling. An unreadable or unhealthy store simply does not match on
	// content; the cheap field match above still stands.
	for _, id := range eventStoreIDs {
		projection, ok := readEventStoreSearchProjection(context.Background(), sessionDir, id)
		if !ok {
			continue
		}
		result.eventStoreProjects[id] = projection
		if projection.matches(needle) {
			if index, ok := recordIndex[id]; ok {
				matched[index] = true
			}
		}
	}

	matchedRecords := make([]dao.SessionRecord, 0, len(records))
	for i := range records {
		if matched[i] {
			matchedRecords = append(matchedRecords, records[i])
		}
	}
	result.total = len(matchedRecords)
	result.page = paginateSessionRecords(matchedRecords, limit, offset)
	return result, nil
}

func newSessionDetailAggregates() dao.SessionDetailAggregates {
	return dao.SessionDetailAggregates{
		MessageCounts: map[string]int{},
		FirstMessages: map[string]string{},
		LatestInfos:   map[string]string{},
	}
}

func paginateSessionRecords(records []dao.SessionRecord, limit, offset int) []dao.SessionRecord {
	if offset > 0 {
		if offset >= len(records) {
			return nil
		}
		records = records[offset:]
	}
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

// sessionRecordCheapMatch evaluates every field that is authoritative without a
// replay: list-row identity/location/channel and the latest session_info title.
// Preview and message text are excluded because compaction and content overrides
// can change or discard them, so they must be matched through buildReplayState.
func sessionRecordCheapMatch(record dao.SessionRecord, aggregates dao.SessionDetailAggregates, needle string) bool {
	if containsFold(record.ID, needle) || containsFold(record.CWD, needle) || containsFold(record.ChannelType, needle) || containsFold(record.ChannelID, needle) {
		return true
	}
	if data := aggregates.LatestInfos[record.ID]; data != "" {
		var entry SessionInfoEntry
		if json.Unmarshal([]byte(data), &entry) == nil && containsFold(entry.Name, needle) {
			return true
		}
	}
	return false
}

// decodeLegacySearchEntry decodes exactly one persisted payload row into the
// entry type replayed by buildReplayState. Unrecognized rows are skipped.
func decodeLegacySearchEntry(entryType, data string) (interface{}, bool) {
	switch EntryType(entryType) {
	case EntryMessage:
		var entry MessageEntry
		if json.Unmarshal([]byte(data), &entry) != nil {
			return nil, false
		}
		return entry, true
	case EntryCompaction:
		var entry CompactionEntry
		if json.Unmarshal([]byte(data), &entry) != nil {
			return nil, false
		}
		return entry, true
	case EntryContentOverride:
		var entry ContentOverrideEntry
		if json.Unmarshal([]byte(data), &entry) != nil {
			return nil, false
		}
		return entry, true
	default:
		return nil, false
	}
}

// replayMessagesMatch checks only the messages that survive content overrides
// and compaction, which is the set GetMessages() exposes.
func replayMessagesMatch(messages []provider.Message, needle string) bool {
	for _, message := range messages {
		if messageMatches(message, needle) {
			return true
		}
	}
	return false
}

func messageMatches(message provider.Message, needle string) bool {
	for _, text := range messageSearchTexts(message) {
		if containsFold(text, needle) {
			return true
		}
	}
	return false
}

// replayPreview returns the truncated first user message of a replayed
// transcript, matching how list details define their preview.
func replayPreview(messages []provider.Message) string {
	for i := range messages {
		message := messages[i]
		if message.Role != "user" {
			continue
		}
		for _, text := range messageSearchTexts(message) {
			if strings.TrimSpace(text) != "" {
				return util.TruncateWithSuffix(text, 60, "...")
			}
		}
	}
	return ""
}

func messageSearchTexts(message provider.Message) []string {
	texts := make([]string, 0, 1+len(message.Contents))
	if message.Content != "" {
		texts = append(texts, message.Content)
	}
	for _, block := range message.Contents {
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return texts
}

func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), needle)
}

// buildSearchDetails turns the scanned page into list details without another
// scan or replay. Legacy preview/title come from the aggregate projection; an
// EventStore page row is completed from the projection already read for
// matching, or read once here when cheap fields matched it before any content
// read was needed.
func buildSearchDetails(sessionDir string, scan sessionSearchScan) []SessionDetail {
	details := make([]SessionDetail, len(scan.page))
	for i, record := range scan.page {
		info := sessionInfosFromRecords(sessionDir, []dao.SessionRecord{record})[0]
		detail := SessionDetail{SessionInfo: info, ID: record.ID, MessageCount: scan.aggregates.MessageCounts[record.ID]}
		if data := scan.aggregates.FirstMessages[record.ID]; data != "" {
			var entry MessageEntry
			if json.Unmarshal([]byte(data), &entry) == nil && entry.Message.Role == "user" {
				for _, text := range messageSearchTexts(entry.Message) {
					if strings.TrimSpace(text) != "" {
						detail.Preview = util.TruncateWithSuffix(text, 60, "...")
						break
					}
				}
			}
		}
		if data := scan.aggregates.LatestInfos[record.ID]; data != "" {
			var entry SessionInfoEntry
			if json.Unmarshal([]byte(data), &entry) == nil {
				detail.Name = entry.Name
			}
		}
		if preview, ok := scan.legacyPreviews[record.ID]; ok && preview != "" {
			detail.Preview = preview
		}
		if record.ContentStore == "event_store" {
			projection, ok := scan.eventStoreProjects[record.ID]
			if !ok {
				projection, ok = readEventStoreSearchProjection(context.Background(), sessionDir, record.ID)
			}
			if ok {
				if detail.Preview == "" {
					detail.Preview = projection.preview
				}
				if detail.Name == "" {
					detail.Name = projection.title
				}
			}
		}
		details[i] = detail
	}
	return details
}

func (projection sessionSearchProjection) matches(needle string) bool {
	if containsFold(projection.title, needle) {
		return true
	}
	for _, text := range projection.texts {
		if containsFold(text, needle) {
			return true
		}
	}
	return false
}

// readEventStoreSearchProjection reads committed EventStore batches once for a
// read-only search projection. It deliberately uses InspectEventStore plus
// committedEventBatches, never convergence, migration, or a lease. Any error or
// unhealthy store reports false so the caller can degrade to cheap matching.
func readEventStoreSearchProjection(ctx context.Context, sessionDir, sessionID string) (sessionSearchProjection, bool) {
	inspection, err := InspectEventStore(ctx, sessionDir, sessionID)
	if err != nil {
		return sessionSearchProjection{}, false
	}
	if inspection.State != EventStoreHealthy && inspection.State != EventStoreReconciling {
		return sessionSearchProjection{}, false
	}
	batches := committedEventBatches(inspection)
	if len(batches) == 0 {
		return sessionSearchProjection{}, false
	}

	entries := make([]interface{}, 0)
	var title string
	for _, batch := range batches {
		for _, record := range batch.Events {
			entry, err := DecodeEventRecord(record)
			if err != nil {
				return sessionSearchProjection{}, false
			}
			if _, ok := entry.(Header); ok {
				continue
			}
			if info, ok := entry.(SessionInfoEntry); ok {
				title = info.Name
			}
			entries = append(entries, entry)
		}
	}

	// Replay once: overrides and compaction determine both the matched text and
	// the preview, exactly as they do for a canonical GetMessages() read.
	state := buildReplayState(entries)
	texts := make([]string, 0, len(state.messages))
	for i := range state.messages {
		texts = append(texts, messageSearchTexts(state.messages[i])...)
	}
	return sessionSearchProjection{title: title, preview: replayPreview(state.messages), texts: texts}, true
}
