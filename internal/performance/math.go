package performance

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func pageAligned(value, page uint64) uint64 {
	if page == 0 || value == 0 {
		return value
	}
	return value - value%page
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func nextPowerOfTwo(n uint64) uint64 {
	if n <= 1 {
		return 1
	}
	if n >= 1<<63 {
		return 1 << 63
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	return n + 1
}

func roundUpMultiple(value, multiple int) int {
	if value <= 0 {
		return 0
	}
	return ((value + multiple - 1) / multiple) * multiple
}
