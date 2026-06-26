package utils

import (
	"context"
	"log"
	"runtime/debug"
	"time"
)

// safeGoRestartBackoff 是后台协程因 panic 中断后，重启前的退避时间。
const safeGoRestartBackoff = time.Second

// SafeGo 在独立 goroutine 中运行 loop，并捕获其 panic，防止单个后台任务的
// panic 拖垮整个进程（Go 中任意 goroutine 未捕获的 panic 都会让进程退出）。
//
// loop 一般是一个 `for { select { <-ctx.Done(): return; ... } }` 循环：
//   - 若 loop 正常返回（通常因 ctx 取消），SafeGo 结束，不再重启；
//   - 若 loop 发生 panic，记录堆栈并在退避后自动重启，直到 ctx 取消。
func SafeGo(ctx context.Context, name string, loop func()) {
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			if !runLoopWithRecover(name, loop) {
				return // 正常返回，无需重启
			}
			log.Printf("background goroutine %q panicked, restarting in %s", name, safeGoRestartBackoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(safeGoRestartBackoff):
			}
		}
	}()
}

// runLoopWithRecover 运行 loop 并捕获 panic。
// 返回 true 表示因 panic 中断（需重启），false 表示正常返回。
func runLoopWithRecover(name string, loop func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC in background goroutine %q: %v\n%s", name, r, debug.Stack())
			panicked = true
		}
	}()
	loop()
	return false
}
