package window

import (
	"github.com/panjf2000/ants/v2"
	"go.uber.org/atomic"
	"log"
	"sync"
	"time"
)

// Meta 事件元数据
type Meta struct {
	AddTime     time.Time // 事件进入窗口时间
	RepeatTimes int64     // 重复的事件次数
}

type UniqueEvent interface {
	Key() string                 // 事件唯一标识，用于去重
	Listen(meta *Meta)           // 事件弹出窗口时调用
	OnDuplicate(new UniqueEvent) // 当事件重复时，回调此方法，可用于合并事件，修改窗口内的事件信息。
}

type NonMerge struct {
}

func (*NonMerge) OnDuplicate(new UniqueEvent) {
	// 默认无需合并
}

type WinEvent struct {
	expireAt    time.Time     // 事件失效时间
	UniqueEvent               // 自定义事件
	next        *WinEvent     // 下一个事件
	repeatTimes *atomic.Int64 // 窗口时间内过滤的重复事件次数，重复事件根据Key() 方法作为判断依据
}

// Window 滑动窗口。buffer未满时，事件会在duration到期时弹出。当buffer已满，会立即弹出最早的事件
type Window struct {
	table         *sync.Map      // 本地事件表，用于快速去重事件
	size          int            // 窗口大小（事件数量）
	duration      time.Duration  // 窗口持续时间
	expiration    *time.Timer    // 定时器，用于过期窗口中最早的事件
	lock, popLock sync.Mutex     // 入窗锁, 出窗锁
	curr, head    *WinEvent      // 当前元素, 窗口头部元素
	buffer        chan *WinEvent // 窗口缓冲池
	pool          *ants.Pool     // pop事件监听池
	config        *Config        // 窗口设置
}

// New 创建一个新的时间滑动窗口。
func New(options ...Option) *Window {
	// 设置默认配置
	config := defaultConfig()
	for _, opt := range options {
		opt(config)
	}
	win := &Window{
		table:    &sync.Map{},
		size:     config.size,
		duration: config.duration,
		buffer:   make(chan *WinEvent, config.size+1), // buffer = size + 1
		config:   config,
	}
	// 开启窗口
	start(win)
	return win
}

// Start 开启窗口
func start(w *Window) {
	// 设置为阻塞任务池时，当消费能力远小于事件发送频率，这里会发生阻塞，入窗也会发生阻塞
	p, _ := ants.NewPool(w.config.poolSize, ants.WithNonblocking(!w.config.block))
	w.pool = p
	// 设置定时器，事件到期时会执行
	w.expiration = time.AfterFunc(w.duration, func() {
		w.pop()
		w.resetExpiration()
	})
	log.Println("滑动窗口初始化完成")
}

// Add 向窗口中添加一个新的事件。
func (w *Window) Add(e UniqueEvent) {
	we := &WinEvent{
		expireAt:    time.Now().Add(w.duration),
		UniqueEvent: e,
		repeatTimes: atomic.NewInt64(0),
	}
	if val, ok := w.table.LoadOrStore(e.Key(), we); ok {
		old := val.(*WinEvent)
		old.repeatTimes.Add(1)
		// 新事件key冲突时回调，此处可以修改窗口事件
		old.OnDuplicate(we)
		return
	}
	w.pushBuffer(we)
}

// 出窗，FIFO原则，弹出最早进入的事件
func (w *Window) pop() *WinEvent {
	w.popLock.Lock()
	defer w.popLock.Unlock()
	var event *WinEvent
	// buffer存在数据时，弹出
	if len(w.buffer) > 0 {
		e := <-w.buffer
		w.head = e.next
		w.table.Delete(e.Key())
		if w.pool != nil {
			err := w.pool.Submit(func() {
				addTime := e.expireAt.Add(-w.duration)
				e.UniqueEvent.Listen(&Meta{
					AddTime:     addTime,
					RepeatTimes: e.repeatTimes.Load(),
				})
			})
			if err != nil {
				log.Printf("Key [%v] 加入执行任务池失败, err:%v", e.Key(), err)
			}
		}
		log.Printf("Key [%v] 弹出窗口", e.Key())
		event = e
	}
	// 数据全部弹出，curr置为nil
	if w.head == nil {
		w.curr = nil
	}
	w.resetExpiration()
	return event
}

// resetExpiration 更新窗口过期定时器，移除过期的事件。
func (w *Window) resetExpiration() {
	// 重置定时器
	if len(w.buffer) > 0 {
		firstEle := w.head
		if firstEle == nil {
			return
		}
		if w.expiration == nil {
			w.expiration = time.AfterFunc(firstEle.expireAt.Sub(time.Now()), func() {
				w.pop()
			})
		} else {
			w.expiration.Reset(firstEle.expireAt.Sub(time.Now()))
		}
	}
}

// Close 关闭窗口，停止定时器。
func (w *Window) Close() {
	w.expiration.Stop()
	w.pool.Release()
}

// 将事件加入buffer池
func (w *Window) pushBuffer(we *WinEvent) {
	w.lock.Lock()
	defer w.lock.Unlock()
	w.buffer <- we
	log.Printf("Key [%v] 已加入执行任务池", we.Key())
	c := w.curr
	if c != nil {
		c.next = we

	} else {
		w.head = we
		if w.expiration == nil {
			w.expiration = time.AfterFunc(we.expireAt.Sub(time.Now()), func() {
				w.pop()
			})
		} else {
			w.expiration.Reset(we.expireAt.Sub(time.Now()))
		}
	}
	w.curr = we
	// 如果缓冲区满了，移除最早的事件
	if len(w.buffer) > w.size {
		w.pop()
	}
}
