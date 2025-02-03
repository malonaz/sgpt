package store

import (
	"sort"
)

func dedupeStringsSorted(strings []string) []string {
	if len(strings) == 0 {
		return strings
	}

	// Create a copy
	copied := make([]string, len(strings))
	copy(copied, strings)

	// Sort the copy
	sort.Strings(copied)

	// Remove duplicates
	j := 0
	for i := 1; i < len(copied); i++ {
		if copied[j] != copied[i] {
			j++
			copied[j] = copied[i]
		}
	}

	return copied[:j+1]
}
