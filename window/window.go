package window

import (
	"github.com/panjf2000/ants/v2"
	"go.uber.org/atomic"
	"log"
	"sync"
	"time"
)

type eventType = uint8

const (
	unique  = eventType(1)
	general = eventType(2)
)

// Meta 事件元数据
type Meta struct {
	AddTime     time.Time // 事件进入窗口时间
	RepeatTimes int64     // 重复的事件次数
}

// UniqueEvent 可去重的事件接口。
type UniqueEvent interface {
	// Key 事件唯一标识，用于去重
	Key() string
	// Listen 事件弹出窗口时调用
	Listen(meta *Meta)
	// OnDuplicate 当事件重复( Key()方法返回的值相同时)，回调此方法，可用于合并事件，修改窗口内的事件信息。
	// new 新加入的事件
	OnDuplicate(new UniqueEvent)
}

type NonMerge struct {
}

func (*NonMerge) OnDuplicate(new UniqueEvent) {
	// 默认无需合并
}

// event 窗口事件
type event struct {
	expireAt    time.Time     // 事件失效时间
	UniqueEvent               // 自定义事件
	next        *event        // 下一个事件
	repeatTimes *atomic.Int64 // 窗口时间内过滤的重复事件次数，重复事件根据Key() 方法作为判断依据
}

// Window 滑动窗口。buffer未满时，事件会在duration到期时弹出。当buffer已满，会立即弹出最早的事件
type Window struct {
	table           *sync.Map     // 本地事件表，用于快速去重事件
	size            int           // 窗口大小（事件数量）
	duration        time.Duration // 窗口持续时间
	expiration      *time.Timer   // 定时器，用于过期窗口中的事件
	inLock, outLock sync.Mutex    // 入窗锁, 出窗锁
	curr, head      *event        // 当前元素, 窗口头部元素
	buffer          chan *event   // 窗口缓冲池
	pool            *ants.Pool    // pop事件监听池
	config          *Config       // 窗口设置
	eventType       eventType
}

// New 创建一个新的时间滑动窗口。
func New(options ...Option) (*Window, error) {
	// 设置默认配置
	config := defaultConfig()
	for _, opt := range options {
		opt(config)
	}
	win := &Window{
		table:    &sync.Map{},
		size:     config.size,
		duration: config.duration,
		buffer:   make(chan *event, config.size+1), // buffer = size + 1
		config:   config,
	}
	// 开启窗口
	err := start(win)
	return win, err
}

// Start 开启窗口
func start(w *Window) error {
	// 设置为阻塞任务池时，当消费能力远小于事件发送频率，入窗会发生阻塞
	p, err := ants.NewPool(w.config.poolSize, ants.WithNonblocking(!w.config.block))
	if err != nil {
		return err
	}
	w.pool = p
	// 设置定时器，事件到期时会执行
	w.expiration = time.AfterFunc(w.duration, func() {
		w.pop()
	})
	log.Println("滑动窗口初始化完成")
	return nil
}

// Add 向窗口中添加一个新的事件。
func (w *Window) Add(e UniqueEvent) {
	we := &event{
		expireAt:    time.Now().Add(w.duration),
		UniqueEvent: e,
		repeatTimes: atomic.NewInt64(0),
	}
	if val, ok := w.table.LoadOrStore(e.Key(), we); ok {
		old := val.(*event)
		old.repeatTimes.Add(1)
		// 新事件key冲突时回调，此处可以修改窗口事件
		old.OnDuplicate(we.UniqueEvent)
		return
	}
	w.pushBuffer(we)
}

// 出窗，FIFO原则，弹出最早进入的事件
func (w *Window) pop() *event {
	w.outLock.Lock()
	defer w.outLock.Unlock()
	var ee *event
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
		ee = e
	}
	// 数据全部弹出，curr置为nil
	if w.head == nil {
		w.curr = nil
	}
	w.resetExpiration()
	return ee
}

// resetExpiration 更新窗口过期定时器，移除过期的事件。
func (w *Window) resetExpiration() {
	// 重置定时器
	if len(w.buffer) > 0 {
		firstElem := w.head
		if firstElem == nil {
			return
		}
		if w.expiration == nil {
			w.expiration = time.AfterFunc(firstElem.expireAt.Sub(time.Now()), func() {
				w.pop()
			})
		} else {
			w.expiration.Reset(firstElem.expireAt.Sub(time.Now()))
		}
	}
}

// Close 关闭窗口，停止定时器。
func (w *Window) Close() {
	w.expiration.Stop()
	w.pool.Release()
}

// 将事件加入buffer池
func (w *Window) pushBuffer(we *event) {
	w.inLock.Lock()
	defer w.inLock.Unlock()
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
