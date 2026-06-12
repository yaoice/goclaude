package teamfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/yaoice/goclaude/pkg/domain/team"
)

// 本文件聚合 inbox 文件的底层 IO：读写、原子写、O_EXCL 锁。
// 从 store.go 拆出以提升可读性；逻辑保持不变。

// ----- 内部：文件 IO -----

func readInboxFile(path string) ([]team.Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read inbox %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var msgs []team.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("decode inbox %s: %w", path, err)
	}
	return msgs, nil
}

func writeInboxFile(path string, msgs []team.Message) error {
	if msgs == nil {
		msgs = []team.Message{}
	}
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return fmt.Errorf("encode inbox: %w", err)
	}
	return atomicWrite(path, data)
}

// atomicWrite 写入 path：先写 path+".tmp.<rand>" 再 rename，保证读者永远看到完整文件。
//
// 调用方需保证 path 的目录已存在。
func atomicWrite(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), rand.Int63())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// acquireLock 用 O_CREATE|O_EXCL 抢哨兵文件。
//
// 失败时按 5/10/20/40/80/160ms 指数退避，附带 ±50% jitter，直到 timeout。
// 返回的 release 必须被调用以删除锁文件；release 是幂等的。
//
// 局限：本机进程 crash 会留下孤儿 lock 文件，下一个抢锁者会一直 timeout。
// 上层（TeamService）在遇到 timeout 时可以提示用户手工 rm <lock>。
// 本函数在超时时会附带打印 lock 文件 mtime/size，方便诊断孤儿。
func acquireLock(lockPath string, timeout time.Duration) (release func(), err error) {
	deadline := time.Now().Add(timeout)
	backoff := 5 * time.Millisecond
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			released := false
			return func() {
				if released {
					return
				}
				released = true
				_ = os.Remove(lockPath)
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			info, statErr := os.Stat(lockPath)
			if statErr == nil {
				age := time.Since(info.ModTime())
				return nil, fmt.Errorf(
					"acquire lock %s: timeout after %s; existing lock age=%s size=%dB (orphan? rm to recover)",
					lockPath, timeout, age.Round(time.Millisecond), info.Size(),
				)
			}
			return nil, fmt.Errorf("acquire lock %s: timeout after %s", lockPath, timeout)
		}
		// jittered backoff: 0.5x ~ 1.5x
		// nolint:gosec  // math/rand 用于 jitter 足够，无需 crypto/rand
		jitter := time.Duration(float64(backoff) * (0.5 + rand.Float64()))
		time.Sleep(jitter)
		if backoff < 160*time.Millisecond {
			backoff *= 2
		}
	}
}
