package window

import (
	"fmt"
	"go.uber.org/atomic"
	"testing"
	"time"
)

type CustomEvent struct {
	ID   string
	Name string
	*NonMerge
}

func (c *CustomEvent) Key() string {
	return c.ID
}

func (c *CustomEvent) Listen(meta *Meta) {
	fmt.Println(c.ID, c.Name, meta.AddTime, meta.RepeatTimes)
}

// Key冲突，将新事件名称赋值给老事件
//func (c *CustomEvent) OnDuplicate(new UniqueEvent) {
//	e := new.(*CustomEvent)
//	c.Name = e.Name
//}

func TestWindow_Add(t *testing.T) {
	win := New(WithBlockPool(3), WithSize(10))
	defer win.Close()

	go func() {
		for i := 0; i < 1000; i++ {
			win.Add(&CustomEvent{
				ID:   fmt.Sprintf("Event-%d", i),
				Name: "window event_01",
			})
		}
	}()

	go func() {
		i := atomic.NewInt64(0)
		for range time.Tick(time.Second * 1) {
			win.Add(
				&CustomEvent{
					ID:   fmt.Sprintf("Timer-%d", i.Load()),
					Name: "window event_02",
				})
			i.Add(1)
		}

	}()

	time.Sleep(time.Second * 10)
}
