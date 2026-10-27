package session

// MessageCount returns the number of messages GetMessages would return for the
// current branch, without cloning provider.Message values. List projections use
// it because they only need the count, while GetMessages deep-copies every
// content block of the transcript.
//
// The count follows buildReplayState/applyCompactionEntry exactly:
//   - content overrides never change the message count;
//   - a compaction summary counts as one message;
//   - a compaction with no FirstKeptEntry replaces the whole history;
//   - a compaction whose FirstKeptEntry is absent is skipped (history kept);
//   - otherwise the first kept entry is located by entry ID.
func (m *Manager) MessageCount() int {
	if m == nil {
		return 0
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return countReplayMessages(m.entries)
}

func countReplayMessages(entries []interface{}) int {
	// entryIDs mirrors replayState.entryIDs, which is the only state compaction
	// needs to locate the first kept entry. It carries "" for a compaction
	// summary entry, exactly like applyCompactionEntry.
	entryIDs := make([]string, 0, len(entries))
	for _, raw := range entries {
		switch entry := raw.(type) {
		case MessageEntry:
			entryIDs = append(entryIDs, entry.ID)
		case CompactionEntry:
			if entry.FirstKeptEntry == "" {
				entryIDs = []string{""}
				continue
			}
			firstKept := -1
			for i, id := range entryIDs {
				if id == entry.FirstKeptEntry {
					firstKept = i
					break
				}
			}
			if firstKept < 0 {
				// Tolerant fallback shared with applyCompactionEntry: the kept
				// entry may belong to a different branch, so keep the history.
				continue
			}
			if firstKept > len(entryIDs) {
				continue
			}
			nextIDs := make([]string, 0, 1+len(entryIDs)-firstKept)
			nextIDs = append(nextIDs, "")
			nextIDs = append(nextIDs, entryIDs[firstKept:]...)
			entryIDs = nextIDs
		}
	}
	return len(entryIDs)
}
