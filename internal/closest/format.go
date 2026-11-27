package closest

import "strconv"

// formatNumber writes n as the question shows it: a year without a
// separator (1969), anything else with thousands separators (12,500).
func formatNumber(n int, format string) string {
	if format == FormatYear {
		return strconv.Itoa(n)
	}
	return thousands(n)
}

// thousands writes n with thousands separators.
func thousands(n int) string {
	if n < 0 {
		return "-" + thousands(-n)
	}
	digits := strconv.Itoa(n)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}
