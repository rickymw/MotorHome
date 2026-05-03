package iracing

import (
	"strconv"
	"strings"
)

// DriverInfo describes one driver entry parsed from the iRacing session YAML's
// Drivers list. An empty UserName means the slot is unpopulated in the YAML
// (ParseDrivers will simply not include that CarIdx).
type DriverInfo struct {
	CarIdx        int32
	UserName      string
	CarScreenName string
	CarNumber     string
	CarClassID    int32
}

// ParseDrivers walks the session info YAML and returns a map keyed by CarIdx
// of every driver block it can find under DriverInfo.Drivers. Any block
// missing a CarIdx is skipped.
//
// The parser is indentation-agnostic — it just tracks "are we inside a driver
// block" by looking for the `- CarIdx:` sentinel. This is the same approach
// used by internal/analysis/lap.go's driverBlockBy{Name,Idx} helpers.
func ParseDrivers(yaml string) map[int32]DriverInfo {
	out := make(map[int32]DriverInfo)
	var cur DriverInfo
	inBlock := false

	flush := func() {
		// Skip empty/sentinel entries. iRacing emits a `- CarIdx: 255` (or
		// other sentinel idx) block with all fields empty to denote "this
		// slot is unpopulated" — including those in the map would show as
		// phantom drivers with blank names.
		if inBlock && cur.UserName != "" {
			out[cur.CarIdx] = cur
		}
	}

	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- CarIdx:") {
			flush()
			inBlock = true
			cur = DriverInfo{}
			idx, ok := parseInt32(strings.TrimSpace(strings.TrimPrefix(trimmed, "- CarIdx:")))
			if !ok {
				inBlock = false
				continue
			}
			cur.CarIdx = idx
			continue
		}
		if !inBlock {
			continue
		}
		// Stop the block at the first un-indented line that isn't a recognised
		// field — e.g. the next top-level key in the YAML.
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' && line[0] != '-' {
			flush()
			inBlock = false
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "UserName:"):
			cur.UserName = yamlValue(trimmed, "UserName:")
		case strings.HasPrefix(trimmed, "CarScreenName:"):
			cur.CarScreenName = yamlValue(trimmed, "CarScreenName:")
		case strings.HasPrefix(trimmed, "CarNumber:"):
			cur.CarNumber = yamlValue(trimmed, "CarNumber:")
		case strings.HasPrefix(trimmed, "CarClassID:"):
			if v, ok := parseInt32(yamlValue(trimmed, "CarClassID:")); ok {
				cur.CarClassID = v
			}
		}
	}
	flush()
	return out
}

// DriverCarIdxFromYAML extracts the DriverCarIdx top-level field.
// Returns -1 if the field is absent or malformed.
func DriverCarIdxFromYAML(yaml string) int32 {
	for _, line := range strings.Split(yaml, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "DriverCarIdx:") {
			if v, ok := parseInt32(yamlValue(trimmed, "DriverCarIdx:")); ok {
				return v
			}
		}
	}
	return -1
}

func yamlValue(trimmedLine, prefix string) string {
	v := strings.TrimSpace(strings.TrimPrefix(trimmedLine, prefix))
	return strings.Trim(v, "\"'")
}

func parseInt32(s string) (int32, bool) {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(n), true
}
