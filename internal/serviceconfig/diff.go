package serviceconfig

import (
	"bytes"
	"fmt"
	"strings"
)

// UnifiedDiff produces a simple unified diff between a and b.
// If a and b are identical, it returns an empty string.
func UnifiedDiff(name string, a, b []byte) string {
	if bytes.Equal(a, b) {
		return ""
	}

	linesA := splitLines(string(a))
	linesB := splitLines(string(b))

	// Simple LCS-based diff
	m := len(linesA)
	n := len(linesB)

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 0; i < m; i++ {
		for j := 0; j < n; j++ {
			if linesA[i] == linesB[j] {
				dp[i+1][j+1] = dp[i][j] + 1
			} else {
				if dp[i+1][j] > dp[i][j+1] {
					dp[i+1][j+1] = dp[i+1][j]
				} else {
					dp[i+1][j+1] = dp[i][j+1]
				}
			}
		}
	}

	type diffLine struct {
		op   byte // ' ', '-', '+'
		text string
	}
	var diffLines []diffLine

	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && linesA[i-1] == linesB[j-1] {
			diffLines = append(diffLines, diffLine{' ', linesA[i-1]})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			diffLines = append(diffLines, diffLine{'+', linesB[j-1]})
			j--
		} else if i > 0 && (j == 0 || dp[i][j-1] < dp[i-1][j]) {
			diffLines = append(diffLines, diffLine{'-', linesA[i-1]})
			i--
		}
	}

	// Reverse diffLines
	for k := 0; k < len(diffLines)/2; k++ {
		opp := len(diffLines) - 1 - k
		diffLines[k], diffLines[opp] = diffLines[opp], diffLines[k]
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- a/%s\n", name)
	fmt.Fprintf(&sb, "+++ b/%s\n", name)
	fmt.Fprintf(&sb, "@@ -1,%d +1,%d @@\n", m, n)
	for _, l := range diffLines {
		sb.WriteByte(l.op)
		sb.WriteString(l.text)
		sb.WriteByte('\n')
	}

	return sb.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}
