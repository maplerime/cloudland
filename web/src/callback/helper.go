package callback

import (
	"strings"
)

func compactSQL(s string) string {
	ss := strings.TrimSpace(s)
	ss = strings.ReplaceAll(ss, "\n", " ")
	ss = strings.ReplaceAll(ss, "\t", " ")
	for strings.Contains(ss, "  ") {
		ss = strings.ReplaceAll(ss, "  ", " ")
	}
	return ss
}
