package window

import "time"

type Config struct {
	size     int           // 窗口大小
	duration time.Duration // 事件失效时间
	poolSize int           // 事件回调任务池大小
	block    bool          // 是否阻塞任务池
}

type Option func(config *Config)

func defaultConfig() *Config {
	return &Config{
		size:     300,
		duration: time.Second * 3,
		poolSize: 20,
		block:    true,
	}
}

// WithSize 窗口最大事件数
func WithSize(size int) Option {
	return func(config *Config) {
		config.size = size
	}
}

// WithDuration 窗口时间大小
func WithDuration(duration time.Duration) Option {
	return func(config *Config) {
		config.duration = duration
	}
}

// WithBlockPool 阻塞类型的事件回调协程池
func WithBlockPool(poolSize int) Option {
	return func(config *Config) {
		config.poolSize = poolSize
		config.block = true
	}
}

// WithNonBlockPool 非阻塞类型的事件回调协程池
func WithNonBlockPool(poolSize int) Option {
	return func(config *Config) {
		config.poolSize = poolSize
		config.block = false
	}
}
