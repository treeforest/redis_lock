// redis_lock/redis_lock.go
package redis_lock

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"errors"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
    ErrLockNotHeld   = errors.New("lock not held or already released")
)

type RedisDistLock struct {
	client           *redis.Client
	key              string
	value            string
	expiration       time.Duration
	mu               sync.Mutex
	stopRenew        chan struct{}
	renewInterval    time.Duration   // 看门狗间隔
	timeout          time.Duration   // 获取锁超时
	ctx              context.Context // 上下文
	isReentrant      bool            // 是否可重入
	holdingGoroutine uint64          // 当前持有锁的 goroutine ID
	reentrantCount   int             // 重入次数
}

// New 创建分布式锁
func New(client *redis.Client, key string, expiration time.Duration, opts ...Option) *RedisDistLock {
	r := &RedisDistLock{
		client:        client,
		key:           key,
		value:         uuid.NewString(),
		expiration:    expiration,
		renewInterval: expiration / 2,
		ctx:           context.Background(),
	}

	// 应用可选参数
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Lock 获取锁（支持可重入）
func (r *RedisDistLock) Lock() (bool, error) {
	return r.LockWithCtx(r.ctx)
}

// LockWithCtx 获取锁（带上下文）
func (r *RedisDistLock) LockWithCtx(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 可重入：如果当前 goroutine 已持有锁
	if r.reentrantCount > 0 && r.isReentrant && r.holdingGoroutine == getGoroutineID() {
		r.reentrantCount++
		return true, nil
	}

	// 尝试获取锁
	var deadline time.Time
	if r.timeout > 0 {
		deadline = time.Now().Add(r.timeout)
	}

	for {
		ok, err := r.client.SetNX(ctx, r.key, r.value, r.expiration).Result()
		if err != nil {
			return false, err
		}
		if ok {
			// 获取成功，启动看门狗
			r.stopRenew = make(chan struct{}, 1)
			go r.renew(ctx, r.stopRenew, r.key, r.expiration, r.renewInterval)
			if r.isReentrant {
				r.holdingGoroutine = getGoroutineID()
				r.reentrantCount = 1
			}
			return true, nil
		}

		// 超时检查
		if r.timeout > 0 && time.Now().After(deadline) {
			return false, nil
		}

		// 避免忙等
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Unlock 释放锁
func (r *RedisDistLock) Unlock() error {
	return r.UnlockWithCtx(r.ctx)
}

// UnlockWithCtx 释放锁（带上下文）
func (r *RedisDistLock) UnlockWithCtx(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 可重入：减少计数
	if r.isReentrant && r.holdingGoroutine == getGoroutineID() {
		if r.reentrantCount == 0 {
			return ErrLockNotHeld
		}
		r.reentrantCount--
		if r.reentrantCount > 0 {
			return nil // 未完全释放
		}
	}

	// 停止看门狗
	if r.stopRenew == nil {
		return ErrLockNotHeld
	}
	close(r.stopRenew) // 关闭通道（触发 renew 协程退出）
	r.stopRenew = nil

	// Lua 脚本释放锁
	script := `
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	else
		return 0
	end
	`
	res, err := r.client.Eval(ctx, script, []string{r.key}, r.value).Int()
	if err != nil {
		return err
	}
	if res == 0 {
		return ErrLockNotHeld
	}
	return nil
}

// renew 看门狗自动续期
func (r *RedisDistLock) renew(ctx context.Context, stopCh chan struct{}, key string, expiration, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_, err := r.client.Expire(ctx, key, expiration).Result()
			if err != nil {
				log.Printf("failed to renew lock %s: %v", r.key, err)
				return
			}
		case <-stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// TryLock 非阻塞获取锁
func (r *RedisDistLock) TryLock(timeout time.Duration) (bool, error) {
	r.timeout = timeout
	return r.Lock()
}

// ==================== 工具函数 ====================

// getGoroutineID 获取 goroutine ID（仅用于可重入锁）
func getGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	idField := strings.Fields(strings.TrimPrefix(string(buf[:n]), "goroutine "))[0]
	id, err := strconv.ParseUint(idField, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("failed to parse goroutine id: %v", err))
	}
	// fmt.Printf("goroutine id: %d\n", id)
	return id
}
