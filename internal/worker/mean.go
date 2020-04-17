package worker

type mean struct {
	sampleSizeShift int
	sample          []int64
	i               int
	sum             int64
}

func (m *mean) Init(simpleSize int, initialValue int64) *mean {
	simpleSize2 := 1

	for simpleSize2 < simpleSize {
		m.sampleSizeShift++
		simpleSize2 += simpleSize2
	}

	m.sample = make([]int64, simpleSize2)

	for i := range m.sample {
		m.sample[i] = initialValue
	}

	m.sum = initialValue << m.sampleSizeShift
	return m
}

func (m *mean) UpdateSample(x int64) {
	y := m.sample[m.i]
	m.sample[m.i] = x
	m.i = (m.i + 1) & ((1 << m.sampleSizeShift) - 1)
	m.sum += x - y
}

func (m *mean) Calculate() int64 {
	return m.sum >> m.sampleSizeShift
}
