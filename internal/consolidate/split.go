package consolidate

import "strings"

// Token counts are estimates: one token per 4 bytes of input.
const bytesPerToken = 4

// EstimateTokens is a rough token estimate: len(s) bytes / 4.
func EstimateTokens(s string) int {
	return len(s) / bytesPerToken
}

// Split groups item indices into parts whose token estimate stays within
// budget; overhead reserves room for the section heading line that repeats
// in every part. Items are grouped greedily in order, and a single item
// larger than the budget becomes its own part.
func Split(items []string, overhead, budgetTokens int) [][]int {
	if len(items) == 0 {
		return nil
	}
	var groups [][]int
	var cur []int
	curTokens := overhead
	for i, item := range items {
		t := EstimateTokens(item) + 1 // the blank line between blocks
		if len(cur) > 0 && curTokens+t > budgetTokens {
			groups = append(groups, cur)
			cur, curTokens = nil, overhead
		}
		cur = append(cur, i)
		curTokens += t
	}
	return append(groups, cur)
}

// partText renders one part: the repeated section heading line followed by
// its blocks.
func partText(name string, blocks []string, group []int) string {
	parts := make([]string, len(group))
	for i, idx := range group {
		parts[i] = blocks[idx]
	}
	return "## " + name + "\n" + strings.Join(parts, "\n\n") + "\n"
}
