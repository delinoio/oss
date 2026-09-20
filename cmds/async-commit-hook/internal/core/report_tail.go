package core

const reportOutputLimit = 64 * 1024

// Keep chronological tail bytes without copying the retained 64 KiB for every
// output event. Grow small test outputs lazily so many short tests do not each
// allocate a full ring. Only a terminal failure materializes the ordered string.
type reportOutputTail struct {
	data  []byte
	start int
}

func (t *reportOutputTail) append(s string) {
	if len(s) >= reportOutputLimit {
		if cap(t.data) < reportOutputLimit {
			t.data = make([]byte, reportOutputLimit)
		} else {
			t.data = t.data[:reportOutputLimit]
		}
		copy(t.data, s[len(s)-reportOutputLimit:])
		t.start = 0
		return
	}
	if len(t.data) < reportOutputLimit {
		n := min(reportOutputLimit-len(t.data), len(s))
		size := len(t.data) + n
		if size > cap(t.data) {
			b := make([]byte, len(t.data), min(reportOutputLimit, max(size, 2*cap(t.data))))
			copy(b, t.data)
			t.data = b
		}
		t.data = append(t.data, s[:n]...)
		s = s[n:]
	}
	if len(s) == 0 {
		return
	}
	n := copy(t.data[t.start:], s)
	copy(t.data, s[n:])
	t.start = (t.start + len(s)) % reportOutputLimit
}
func (t *reportOutputTail) String() string {
	if t == nil {
		return ""
	}
	if t.start == 0 {
		return string(t.data)
	}
	ordered := make([]byte, len(t.data))
	n := copy(ordered, t.data[t.start:])
	copy(ordered[n:], t.data[:t.start])
	return string(ordered)
}
