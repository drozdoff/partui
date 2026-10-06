package par2

// CreateOptions holds the options for PAR2 creation. The zero-value fields
// follow the gopar conventions partui relies on: SliceByteCount and
// NumGoroutines <= 0 mean "implementation default", NumParityShards <= 0
// means the default shard count (3).
//
// The streaming create itself lands in stage 2 of plans/streaming_file.md;
// until then the app runner forwards these to gopar.
type CreateOptions struct {
	// SliceByteCount is how big each slice should be in bytes.
	SliceByteCount int
	// NumParityShards is the number of parity shards to create; -1 selects
	// the default count.
	NumParityShards int
	// NumGoroutines parallelises the parity computation.
	NumGoroutines int
}
