# redis_lock

一个基于 Redis 的分布式锁实现，支持可重入、自动续期（看门狗）、超时控制等功能，适用于 Go 语言项目中的分布式并发控制场景。

## 特性

- 基本分布式锁功能：基于 Redis 的 SET NX 命令实现
- 可重入支持：同一 goroutine 可多次获取同一把锁
- 自动续期：内置看门狗机制自动延长锁的过期时间，防止业务未完成时锁提前释放
- 超时控制：支持设置获取锁的超时时间
- 上下文支持：可通过 context 控制锁的获取过程
- 安全释放：使用 Lua 脚本保证释放锁的原子性，避免误释放其他客户端的锁

## 安装

```bash
go get github.com/treeforest/redis_lock
```

## 依赖

- Redis
- Go 1.18+（更低版本可能兼容，但未测试）

## 快速开始

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/treeforest/redis_lock"
)

func main() {
	// 初始化 Redis 客户端
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})

	// 创建分布式锁，过期时间 5 秒
	lock := redis_lock.New(rdb, "my_lock_key", 5*time.Second)
	
	// 获取锁
	ok, err := lock.Lock()
	if err != nil {
		fmt.Printf("获取锁失败: %v\n", err)
		return
	}
	if !ok {
		fmt.Println("获取锁超时")
		return
	}
	defer lock.Unlock() // 确保释放锁

	// 执行临界区操作
	fmt.Println("获取锁成功，执行临界区操作...")
	time.Sleep(3 * time.Second)
}
```

## 高级用法

### 可重入锁

```go
// 创建可重入锁
lock := redis_lock.New(rdb, "reentrant_lock", 5*time.Second, redis_lock.WithReentrant())

// 第一次获取
ok, _ := lock.Lock()
// 第二次获取（同一 goroutine）
ok, _ = lock.Lock()

// 需要释放相同次数
lock.Unlock()
lock.Unlock() // 真正释放
```

### 自定义看门狗续期间隔

```go
// 自定义续期间隔为 1 秒（默认是过期时间的一半）
lock := redis_lock.New(rdb, "custom_renew_lock", 5*time.Second, 
    redis_lock.WithRenewInterval(1*time.Second))
```

### 设置获取锁超时

```go
// 设置获取锁的超时时间为 2 秒
lock := redis_lock.New(rdb, "timeout_lock", 5*time.Second, 
    redis_lock.WithTimeout(2*time.Second))
```

### 使用自定义上下文

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()

// 使用自定义上下文
lock := redis_lock.New(rdb, "ctx_lock", 5*time.Second, 
    redis_lock.WithContext(ctx))
ok, err := lock.Lock()
```

### 非阻塞获取锁

```go
// 尝试获取锁，最多等待 1 秒
ok, err := lock.TryLock(1*time.Second)
```

## 测试

项目包含完整的测试用例，测试前请确保本地 Redis 服务已启动（默认地址 localhost:6379）。

```bash
# 运行所有测试
go test -v

# 运行基准测试
go test -bench=. -benchmem
```

测试用例涵盖：
- 基础功能测试
- 并发竞争测试
- 可重入锁测试
- 超时控制测试
- 看门狗续期测试
- 异常场景处理测试