package session

import (
	"testing"

	"github.com/oschina/mothx/internal/provider"
)

func TestManagerMessageCountMatchesReplay(t *testing.T) {
	message := func(id, text string) MessageEntry {
		return MessageEntry{EntryBase: EntryBase{Type: EntryMessage, ID: id}, Message: provider.NewUserMessage(text)}
	}
	override := func(target, text string) ContentOverrideEntry {
		return ContentOverrideEntry{EntryBase: EntryBase{Type: EntryContentOverride, ID: "override-" + target}, TargetEntryID: target, Message: provider.NewUserMessage(text)}
	}
	compaction := func(id, firstKept string) CompactionEntry {
		return CompactionEntry{EntryBase: EntryBase{Type: EntryCompaction, ID: id}, Summary: "summary", FirstKeptEntry: firstKept}
	}

	tests := []struct {
		name    string
		entries []interface{}
		want    int
	}{
		{name: "empty", want: 0},
		{
			name:    "plain messages",
			entries: []interface{}{message("m1", "one"), message("m2", "two"), message("m3", "three")},
			want:    3,
		},
		{
			name:    "content override does not change count",
			entries: []interface{}{message("m1", "one"), message("m2", "two"), override("m2", "replaced")},
			want:    2,
		},
		{
			name:    "single compaction keeps first kept onward",
			entries: []interface{}{message("m1", "one"), message("m2", "two"), message("m3", "three"), compaction("c1", "m2")},
			want:    3,
		},
		{
			name:    "compaction without first kept resets history",
			entries: []interface{}{message("m1", "one"), message("m2", "two"), compaction("c1", "")},
			want:    1,
		},
		{
			name:    "compaction with unknown first kept is skipped",
			entries: []interface{}{message("m1", "one"), message("m2", "two"), compaction("c1", "missing")},
			want:    2,
		},
		{
			name: "multiple compactions",
			entries: []interface{}{
				message("m1", "one"), message("m2", "two"), message("m3", "three"), message("m4", "four"),
				compaction("c1", "m3"), // summary + m3 + m4
				message("m5", "five"),
				compaction("c2", "m5"), // summary + m5
			},
			want: 2,
		},
		{
			name:    "compaction targeting an earlier summary is skipped",
			entries: []interface{}{message("m1", "one"), compaction("c1", ""), compaction("c2", "c1")},
			want:    1,
		},
		{
			name:    "trailing message after compaction",
			entries: []interface{}{message("m1", "one"), compaction("c1", ""), message("m2", "two")},
			want:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{entries: tt.entries}
			got := m.MessageCount()
			if got != tt.want {
				t.Fatalf("MessageCount() = %d, want %d", got, tt.want)
			}
			if replayed := len(m.GetMessages()); got != replayed {
				t.Fatalf("MessageCount() = %d, len(GetMessages()) = %d", got, replayed)
			}
		})
	}
}
