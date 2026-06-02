package shell

import (
	"errors"
	"io"
	"os"

	"golang.org/x/term"
)

// Terminal 封装 STDIN 原始模式切换 + 窗口尺寸读取
//
// 进入原始模式后，read 是按字节阻塞读，不行缓冲、不回显，
// 这样我们才能拦截 ↑/↓/Tab/Ctrl-* 等控制键。
type Terminal struct {
	fd         int
	oldState   *term.State
	rawEnabled bool

	// 输入流（默认 os.Stdin）；测试中可注入。
	in io.Reader
}

// NewTerminal 构造 Terminal；默认 fd 为 stdin
func NewTerminal() *Terminal {
	return &Terminal{
		fd: int(os.Stdin.Fd()),
		in: os.Stdin,
	}
}

// IsTerminal 判断 stdin 是否连接到真实终端
func (t *Terminal) IsTerminal() bool {
	return term.IsTerminal(t.fd)
}

// EnterRaw 进入原始模式
//
// 幂等：已在原始模式时直接返回。
// 非终端时返回 ErrNotATerminal。
func (t *Terminal) EnterRaw() error {
	if t.rawEnabled {
		return nil
	}
	if !t.IsTerminal() {
		return ErrNotATerminal
	}
	state, err := term.MakeRaw(t.fd)
	if err != nil {
		return err
	}
	t.oldState = state
	t.rawEnabled = true
	return nil
}

// LeaveRaw 退出原始模式（恢复原 termios）
//
// 幂等：未在原始模式时直接返回。
func (t *Terminal) LeaveRaw() error {
	if !t.rawEnabled || t.oldState == nil {
		return nil
	}
	err := term.Restore(t.fd, t.oldState)
	t.rawEnabled = false
	t.oldState = nil
	return err
}

// Size 返回 (width, height)；失败时返回 (80, 24) 兜底
func (t *Terminal) Size() (int, int) {
	w, h, err := term.GetSize(t.fd)
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// WithCookedMode 暂时离开原始模式，执行 fn 期间使用规范（cooked）模式，
// 让 stdin 上的 ReadString('\n') 等行缓冲读能正常工作（AskUser 工具需要）。
//
// 不在 raw 模式时，fn 直接执行；fn 结束自动恢复到调用前的状态。
func (t *Terminal) WithCookedMode(fn func() error) error {
	wasRaw := t.rawEnabled
	if wasRaw {
		if err := t.LeaveRaw(); err != nil {
			return err
		}
	}
	defer func() {
		if wasRaw {
			_ = t.EnterRaw()
		}
	}()
	return fn()
}

// ReadByte 读一个字节（用于按键解析）
func (t *Terminal) ReadByte() (byte, error) {
	var buf [1]byte
	n, err := t.in.Read(buf[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return buf[0], nil
}

// ErrNotATerminal 输入不是真实终端时返回
var ErrNotATerminal = errors.New("stdin is not a terminal")
