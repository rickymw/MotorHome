package pb

import (
	"encoding/json"
	"os"
	"time"
)

// ScanStamp identifies one version of a telemetry file: the same name with a
// different size or modification time is a different file as far as the
// ledger is concerned (most often, a session that was still being recorded
// the last time it was looked at).
type ScanStamp struct {
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// Scanned is the ledger of .ibt files whose laps have already been checked
// against the personal bests, keyed by file basename. It is stored in
// pbscan.json next to pb.json.
//
// It exists because analyze only ever looks at one session: a session nobody
// ran analyze on could hold a lap faster than the stored PB, and nothing would
// ever notice. The ledger is what lets the catch-up check skip every file it
// has already read.
type Scanned map[string]ScanStamp

// LoadScanned reads the ledger at path. A missing file is an empty ledger —
// that is the first-run state, in which every session gets checked once.
func LoadScanned(path string) (Scanned, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Scanned{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s Scanned
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s == nil {
		s = Scanned{}
	}
	return s, nil
}

// SaveScanned writes the ledger atomically.
func SaveScanned(path string, s Scanned) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b)
}

// Seen reports whether name has been checked at exactly this size and
// modification time.
func (s Scanned) Seen(name string, size int64, modTime time.Time) bool {
	st, ok := s[name]
	return ok && st.Size == size && st.ModTime.Equal(modTime)
}

// Mark records name as checked at this size and modification time.
func (s Scanned) Mark(name string, size int64, modTime time.Time) {
	s[name] = ScanStamp{Size: size, ModTime: modTime}
}
