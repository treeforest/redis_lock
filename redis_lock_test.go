package redis_lock

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// 初始化 Redis 客户端
func newTestClient(t *testing.T) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   1, // 使用 DB 1 避免影响其他数据
	})

	// 清理测试数据
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("failed to flush db: %v", err)
	}
	return rdb
}


// TestLock_Basic 基础功能测试
func TestLock_Basic(t *testing.T) {
	rdb := newTestClient(t)
	lock := New(rdb, "basic_lock", 5*time.Second, WithTimeout(2*time.Second))

	// 第一次获取锁
	ok, err := lock.Lock()
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.NoError(t, lock.Unlock())

	// 第二次获取锁（应成功）
	ok, err = lock.Lock()
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.NoError(t, lock.Unlock())
}

// TestLock_Concurrent 并发竞争测试
func TestLock_Concurrent_Mutex(t *testing.T) {
    rdb := newTestClient(t)
    const numGoroutines = 10
    var (
        activeCount int32 // 当前持有锁的协程数
        maxActive   int32 // 同时持有锁的最大值（应 == 1）
        wg          sync.WaitGroup
    )

    for i := 0; i < numGoroutines; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            lock := New(rdb, "mutex_lock", 5*time.Second, WithTimeout(2*time.Second))

            if ok, err := lock.Lock(); err == nil && ok {
                defer lock.Unlock()

                // ✅ 关键：进入临界区
                count := atomic.AddInt32(&activeCount, 1)
                if count > 1 {
                    t.Errorf("❌ 协程 %d: 同时持有锁的协程数 = %d", id, count)
                }
                if count > atomic.LoadInt32(&maxActive) {
                    atomic.StoreInt32(&maxActive, count)
                }

                // 模拟工作
                time.Sleep(100 * time.Millisecond)

                // 离开临界区
                atomic.AddInt32(&activeCount, -1)
            }
        }(i)
    }
    wg.Wait()

    // ✅ 验证：最大同时持有锁的协程数 <= 1
    assert.LessOrEqual(t, atomic.LoadInt32(&maxActive), int32(1))
    t.Logf("✅ 最大同时持有锁的协程数: %d", atomic.LoadInt32(&maxActive))
}

// TestLock_Reentrant 可重入锁测试
func TestLock_Reentrant(t *testing.T) {
	rdb := newTestClient(t)
	lock := New(rdb, "reentrant_lock", 5*time.Second, WithReentrant())

	// 第一次获取
	ok, err := lock.Lock()
	assert.NoError(t, err)
	assert.True(t, ok)

	// 第二次获取（同一 goroutine）
	ok, err = lock.Lock()
	assert.NoError(t, err)
	assert.True(t, ok)

	// 释放一次，不应真正释放
	assert.NoError(t, lock.Unlock())
	// 检查锁是否还存在
	val, err := rdb.Get(context.Background(), "reentrant_lock").Result()
	assert.NoError(t, err)
	assert.NotEmpty(t, val)

	// 释放第二次，真正释放
	assert.NoError(t, lock.Unlock())
	_, err = rdb.Get(context.Background(), "reentrant_lock").Result()
	assert.Equal(t, redis.Nil, err)
}

// TestLock_Timeout 锁获取超时测试
func TestLock_Timeout(t *testing.T) {
	rdb := newTestClient(t)
	lock1 := New(rdb, "timeout_lock", 2*time.Second)
	lock2 := New(rdb, "timeout_lock", 2*time.Second, WithTimeout(1*time.Second))

	// 第一个锁持有
	ok, err := lock1.Lock()
	assert.NoError(t, err)
	assert.True(t, ok)

	// 第二个锁尝试获取，应超时失败
	ok, err = lock2.Lock()
	assert.NoError(t, err)
	assert.False(t, ok)

	assert.NoError(t, lock1.Unlock())
}

// TestLock_Renewal 看门狗续期测试
func TestLock_Renewal(t *testing.T) {
	rdb := newTestClient(t)
	lock := New(rdb, "renewal_lock", 3*time.Second, WithRenewInterval(1*time.Second))

	ok, err := lock.Lock()
	assert.NoError(t, err)
	assert.True(t, ok)

	// 等待 5 秒（超过 TTL，但看门狗应续期）
	time.Sleep(5 * time.Second)

	// 检查锁是否还存在
	val, err := rdb.Get(context.Background(), "renewal_lock").Result()
	assert.NoError(t, err)
	assert.Equal(t, lock.value, val)

	// 释放锁
	assert.NoError(t, lock.Unlock())
	_, err = rdb.Get(context.Background(), "renewal_lock").Result()
	assert.Equal(t, redis.Nil, err)
}

// TestLock_ContextTimeout 上下文超时测试
func TestLock_ContextTimeout(t *testing.T) {
	rdb := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// 模拟锁被占用
	rdb.Set(ctx, "ctx_lock", "other", 3*time.Second)

	lock := New(rdb, "ctx_lock", 3*time.Second, WithContext(ctx))
	ok, err := lock.LockWithCtx(ctx)
	assert.Error(t, err) // 应返回 context deadline exceeded
	assert.False(t, ok)
}

// TestLock_ReleaseFailure 锁释放失败处理（模拟 Redis 故障）
func TestLock_ReleaseFailure(t *testing.T) {
	rdb := newTestClient(t)
	lock := New(rdb, "fail_release_lock", 5*time.Second)

	ok, err := lock.Lock()
	assert.NoError(t, err)
	assert.True(t, ok)

	// 模拟 Redis 断开连接
	rdb.Close()

	// 释放锁（应失败，但不应 panic）
	err = lock.Unlock()
	assert.Error(t, err)
	t.Logf("✅ 锁释放失败（预期）: %v", err)
}

// TestLock_ReacquireAfterExpiry 锁过期场景
func TestLock_ExpiryScenarios(t *testing.T) {
    t.Run("1. 看门狗正常，锁永不过期", func(t *testing.T) {
        rdb := newTestClient(t)
        lock := New(rdb, "wd_alive", 2*time.Second, WithRenewInterval(1*time.Second))
        ok, _ := lock.Lock()
        assert.True(t, ok)
        time.Sleep(5 * time.Second) // 看门狗续期 5 次
        val, _ := rdb.Get(context.Background(), "wd_alive").Result()
        assert.Equal(t, lock.value, val) // ✅ 锁依然存在
        lock.Unlock()
    })

    t.Run("2. 看门狗停止，锁过期", func(t *testing.T) {
        rdb := newTestClient(t)
        lock := New(rdb, "wd_stop", 2*time.Second, WithRenewInterval(1*time.Second))
        ok, _ := lock.Lock()
        assert.True(t, ok)
		lock.stopRenew <- struct{}{} // 手动停止
        time.Sleep(3 * time.Second)
        _, err := rdb.Get(context.Background(), "wd_stop").Result()
        assert.Equal(t, redis.Nil, err) // ✅ 锁已过期
    })

    t.Run("3. 锁过期后可重新获取", func(t *testing.T) {
        rdb := newTestClient(t)
        lock := New(rdb, "reacquire", 2*time.Second)
        ok, _ := lock.Lock()
        assert.True(t, ok)
		lock.stopRenew <- struct{}{} // 手动停止
        time.Sleep(3 * time.Second)
        ok, _ = lock.Lock()
        assert.True(t, ok, "应能重新获取过期锁")
        lock.Unlock()
    })
}

// TestLock_ReadWritePattern 模拟读写锁（共享/排他）
func TestLock_ReadWritePattern(t *testing.T) {
	rdb := newTestClient(t)
	var wg sync.WaitGroup
	const numReaders = 5
	var sharedValue int32

	// 写锁
	writeLock := New(rdb, "rw:write", 5*time.Second)
	// 读锁（使用不同 key，但实际可用同一 key 的 SET NX 实现共享锁）
	readLock := New(rdb, "rw:read", 3*time.Second, WithTimeout(1*time.Second))

	// 写操作（排他）
	wg.Add(1)
	go func() {
		defer wg.Done()
		if ok, _ := writeLock.Lock(); ok {
			defer writeLock.Unlock()
			time.Sleep(100 * time.Millisecond)
			atomic.StoreInt32(&sharedValue, 42)
			t.Log("✍️ 写操作完成")
		}
	}()

	// 读操作（共享，但这里简化为互斥，实际可用 Redlock 或 Lua 实现真共享锁）
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if ok, _ := readLock.Lock(); ok {
				defer readLock.Unlock()
				val := atomic.LoadInt32(&sharedValue)
				t.Logf("📖 协程 %d 读到值: %d", id, val)
			}
		}(i)
	}

	wg.Wait()
}

// TestLock_Performance 性能测试（基准测试）
func BenchmarkLock_Acquire(b *testing.B) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		DB:       2,
	})
	defer rdb.FlushDB(context.Background())

	lock := New(rdb, "bench_lock", 10*time.Second, WithRenewInterval(2*time.Second))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ok, _ := lock.Lock(); ok {
			//time.Sleep(1 * time.Millisecond) // 模拟工作
			_ = lock.Unlock()
		}
	}
}

// TestLock_MultipleKeys 多 key 并发测试
func TestLock_MultipleKeys(t *testing.T) {
	rdb := newTestClient(t)
	var wg sync.WaitGroup
	keys := []string{"key1", "key2", "key3"}

	for i, key := range keys {
		wg.Add(1)
		go func(id int, k string) {
			defer wg.Done()
			lock := New(rdb, k, 3*time.Second)
			if ok, _ := lock.Lock(); ok {
				defer lock.Unlock()
				t.Logf("🔑 协程 %d 持有锁 %s", id, k)
				time.Sleep(20 * time.Millisecond)
			}
		}(i, key)
	}
	wg.Wait()
}