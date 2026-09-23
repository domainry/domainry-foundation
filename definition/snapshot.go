package definition

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"sort"
)

// SnapshotRevision returns the canonical content revision for the active
// Definition identities in a snapshot. Source metadata and publication times
// are intentionally excluded: republishing identical effective content does
// not change the revision.
func SnapshotRevision(snapshot Snapshot) string {
	values := append([]Definition(nil), snapshot.Definitions...)
	sort.Slice(values, func(left, right int) bool {
		if values[left].Owner != values[right].Owner {
			return values[left].Owner < values[right].Owner
		}
		if values[left].ResourceType != values[right].ResourceType {
			return values[left].ResourceType < values[right].ResourceType
		}
		return values[left].ResourceKey < values[right].ResourceKey
	})
	digest := sha256.New()
	for _, value := range values {
		writeRevisionField(digest, value.Owner)
		writeRevisionField(digest, value.ResourceType)
		writeRevisionField(digest, value.ResourceKey)
		writeRevisionField(digest, value.SchemaHash)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeRevisionField(digest hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write([]byte(value))
}
