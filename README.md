## 描述：
滑动窗口，可用于消息去重，延迟消费，流量控制等场景.
## 使用方法:
```go
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

func main() {
    win := window.New()
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
