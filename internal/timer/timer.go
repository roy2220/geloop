package timer

import (
	"time"
	"unsafe"

	"github.com/roy2220/intrusive"
)

// Timer ...
type Timer struct {
	alarmHeap intrusive.Heap
}

// Init ...
func (t *Timer) Init() *Timer {
	t.alarmHeap.Init(func(heapNode1, heapNode2 *intrusive.HeapNode) bool {
		alarm1 := (*Alarm)(heapNode1.GetContainer(unsafe.Offsetof(Alarm{}.heapNode)))
		alarm2 := (*Alarm)(heapNode2.GetContainer(unsafe.Offsetof(Alarm{}.heapNode)))
		return alarm1.dueTime.Before(alarm2.dueTime)
	}, initialAlarmHeapCapacity)

	return t
}

// Close ...
func (t *Timer) Close(callback func(*Alarm)) {
	for heapTop, ok := t.alarmHeap.GetTop(); ok; heapTop, ok = t.alarmHeap.GetTop() {
		t.alarmHeap.RemoveNode(heapTop)
		alarm := (*Alarm)(heapTop.GetContainer(unsafe.Offsetof(Alarm{}.heapNode)))
		*alarm = Alarm{}
		callback(alarm)
	}

	t.alarmHeap = intrusive.Heap{}
}

// AddAlarm ...
func (t *Timer) AddAlarm(alarm *Alarm, dueTime time.Time) {
	alarm.dueTime = dueTime
	t.alarmHeap.InsertNode(&alarm.heapNode)
}

// RemoveAlarm ...
func (t *Timer) RemoveAlarm(alarm *Alarm) {
	t.alarmHeap.RemoveNode(&alarm.heapNode)
	*alarm = Alarm{}
}

// ProcessAlarms ...
func (t *Timer) ProcessAlarms(callback func(*Alarm)) {
	now := time.Now()

	for heapTop, ok := t.alarmHeap.GetTop(); ok; heapTop, ok = t.alarmHeap.GetTop() {
		alarm := (*Alarm)(heapTop.GetContainer(unsafe.Offsetof(Alarm{}.heapNode)))

		if alarm.dueTime.After(now) {
			return
		}

		t.alarmHeap.RemoveNode(heapTop)
		*alarm = Alarm{}
		callback(alarm)
	}
}

// GetMinDueTime ...
func (t *Timer) GetMinDueTime() (time.Time, bool) {
	heapTop, ok := t.alarmHeap.GetTop()

	if !ok {
		return time.Time{}, false
	}

	alarm := (*Alarm)(heapTop.GetContainer(unsafe.Offsetof(Alarm{}.heapNode)))
	return alarm.dueTime, true
}

// Alarm ...
type Alarm struct {
	heapNode intrusive.HeapNode
	dueTime  time.Time
}

// IsReset ...
func (a *Alarm) IsReset() bool { return a.heapNode.IsReset() }

const initialAlarmHeapCapacity = 64
