// redis_lock/option.go
package redis_lock

import (
	"context"
	"time"
)

// Option 配置函数类型
type Option func(*RedisDistLock)

// WithReentrant 启用可重入锁（基于 goroutine ID）
func WithReentrant() Option {
	return func(r *RedisDistLock) {
		r.isReentrant = true
		r.holdingGoroutine = getGoroutineID()
	}
}

// WithRenewInterval 自定义看门狗续期间隔（默认 TTL/2）
func WithRenewInterval(interval time.Duration) Option {
	return func(r *RedisDistLock) {
		r.renewInterval = interval
	}
}

// WithTimeout 获取锁的超时时间（默认无超时）
func WithTimeout(timeout time.Duration) Option {
	return func(r *RedisDistLock) {
		r.timeout = timeout
	}
}

// WithContext 使用自定义 context（默认 context.Background()）
func WithContext(ctx context.Context) Option {
	return func(r *RedisDistLock) {
		r.ctx = ctx
	}
}

// WithValue 自定义锁的唯一值（默认 UUID）
func WithValue(value string) Option {
	return func(r *RedisDistLock) {
		if value == "" {
            panic("lock value cannot be empty")
        }
		r.value = value
	}
}
