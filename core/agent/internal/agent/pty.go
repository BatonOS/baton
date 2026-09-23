// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/batonos/baton/core/agent/internal/protocol"
)








const (
	ioctlTIOCSPTLCK = 0x40045431
	ioctlTIOCGPTN   = 0x80045430
	ioctlTIOCSWINSZ = 0x5414
)

type winsize struct{ Row, Col, Xpixel, Ypixel uint16 }

type ptySession struct {
	master *os.File
	cmd    *exec.Cmd
}




func openPTY() (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), ioctlTIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, nil, e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), ioctlTIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, nil, e
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}

func setWinsize(f *os.File, cols, rows int) {
	ws := winsize{Row: uint16(rows), Col: uint16(cols)}
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlTIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}



func ptyShell() []string {
	if _, err := os.Stat("/bin/bash"); err == nil {
		return []string{"/bin/bash", "-l"}
	}
	return []string{"/bin/sh", "-l"}
}



func (a *Agent) handlePtyOpen(ctx context.Context, conn *protocol.Conn, req protocol.PtyOpen) {
	if req.Session == "" {
		return
	}



	if !RemoteShellAllowed(a.cfg.DataDir) {
		a.logger.Info("pty refused: remote shell disabled", "session", req.Session)
		_ = conn.Send(ctx, protocol.TypePtyClose, a.seq.Add(1), protocol.PtyClose{
			Session: req.Session,
			Reason:  "remote shell is disabled on this node (baton agent shell <name> on)",
		})
		return
	}
	a.ptyMu.Lock()
	if a.ptys == nil {
		a.ptys = map[string]*ptySession{}
	}
	if _, exists := a.ptys[req.Session]; exists {
		a.ptyMu.Unlock()
		return
	}
	a.ptyMu.Unlock()

	master, slave, err := openPTY()
	if err != nil {
		a.logger.Error("pty open", "error", err)
		_ = conn.Send(ctx, protocol.TypePtyClose, a.seq.Add(1), protocol.PtyClose{Session: req.Session, Reason: "could not allocate a terminal"})
		return
	}
	argv := ptyShell()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if req.Cols > 0 && req.Rows > 0 {
		setWinsize(master, req.Cols, req.Rows)
	}
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		_ = conn.Send(ctx, protocol.TypePtyClose, a.seq.Add(1), protocol.PtyClose{Session: req.Session, Reason: "could not start the shell"})
		return
	}
	slave.Close()

	sess := &ptySession{master: master, cmd: cmd}
	a.ptyMu.Lock()
	a.ptys[req.Session] = sess
	a.ptyMu.Unlock()
	a.logger.Info("pty opened", "session", req.Session, "shell", argv[0])


	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				_ = conn.Send(ctx, protocol.TypePtyData, a.seq.Add(1), protocol.PtyData{
					Session: req.Session,
					Data:    base64.StdEncoding.EncodeToString(buf[:n]),
				})
			}
			if err != nil {
				break
			}
		}
		_ = cmd.Wait()
		a.closePty(ctx, conn, req.Session, "shell exited")
	}()
}

func (a *Agent) handlePtyData(req protocol.PtyData) {
	a.ptyMu.Lock()
	sess := a.ptys[req.Session]
	a.ptyMu.Unlock()
	if sess == nil {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		return
	}
	_, _ = sess.master.Write(data)
}

func (a *Agent) handlePtyResize(req protocol.PtyResize) {
	a.ptyMu.Lock()
	sess := a.ptys[req.Session]
	a.ptyMu.Unlock()
	if sess != nil && req.Cols > 0 && req.Rows > 0 {
		setWinsize(sess.master, req.Cols, req.Rows)
	}
}


func (a *Agent) closePty(ctx context.Context, conn *protocol.Conn, session, reason string) {
	a.ptyMu.Lock()
	sess := a.ptys[session]
	if sess != nil {
		delete(a.ptys, session)
	}
	a.ptyMu.Unlock()
	if sess == nil {
		return
	}
	if sess.cmd != nil && sess.cmd.Process != nil {
		_ = sess.cmd.Process.Kill()
	}
	_ = sess.master.Close()
	if conn != nil {
		_ = conn.Send(ctx, protocol.TypePtyClose, a.seq.Add(1), protocol.PtyClose{Session: session, Reason: reason})
	}
}

