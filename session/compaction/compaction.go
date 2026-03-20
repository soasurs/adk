// Package compaction provides types for manual context management.
// Users are responsible for deciding when and how to compact context;
// the SDK only provides CompactMessages to persist the compaction state.
package compaction

// Config is provided for reference by users implementing custom compaction logic.
// It defines the parameters a compaction strategy might use.
type Config struct {
	// MaxTokens can be used by custom compaction logic as a threshold.
	MaxTokens int64
	// KeepRecentRounds can be used by custom compaction logic to define
	// how many recent conversation rounds to preserve before archiving.
	KeepRecentRounds int
}
