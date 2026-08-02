package livesync

import "strings"

// IsMetadataLocalDoc reports whether a _local document is livesync's own.
//
// A database's _local space holds two unrelated things: livesync's milestone
// and encryption parameters, and one checkpoint per replication that has ever
// run against it. Only the first kind belongs to the vault.
//
// Copying a checkpoint to another server would be worse than not copying it:
// it records how far a replication got in *that* database's change sequence,
// so a client reading it on a different server concludes it has already synced
// changes it has never seen.
//
// Both spellings appear in the wild. The milestone document has carried the
// original "obsydian" typo since livesync's early versions; later documents use
// the correct one.
func IsMetadataLocalDoc(id string) bool {
	return strings.HasPrefix(id, "_local/obsidian_livesync") ||
		strings.HasPrefix(id, "_local/obsydian_livesync")
}
