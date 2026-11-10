//go:build linux

package browserbridge

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

func SocketPath() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		var e error
		base, e = os.UserCacheDir()
		if e != nil {
			return "", e
		}
	}
	dir := filepath.Join(base, "fluxget-browser")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("browser IPC directory is not private")
	}
	path := filepath.Join(dir, "owner.sock")
	if len(path) > 100 {
		return "", errors.New("browser IPC path is too long")
	}
	return path, nil
}
func checkPeer(conn *net.UnixConn) error {
	raw, e := conn.SyscallConn()
	if e != nil {
		return e
	}
	var check error
	e = raw.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			check = err
		} else if cred.Uid != uint32(os.Getuid()) {
			check = errors.New("browser IPC peer is not the current user")
		}
	})
	if e != nil {
		return e
	}
	return check
}

type Server struct {
	mu          sync.Mutex
	connections map[*net.UnixConn]bool
	listener    *net.UnixListener
	lock        *os.File
	stop        context.CancelFunc
	done        chan struct{}
	once        sync.Once
}

func Serve(handle func(context.Context, Request) Response) (*Server, error) {
	path, e := SocketPath()
	if e != nil {
		return nil, e
	}
	lock, e := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, errors.New("browser bridge already has an owner")
	}
	fail := func(err error) (*Server, error) { lock.Close(); return nil, err }
	if info, e := os.Lstat(path); e == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSocket == 0 || stat.Uid != uint32(os.Getuid()) {
			return fail(errors.New("unexpected browser IPC entry"))
		}
		if e = os.Remove(path); e != nil {
			return fail(e)
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return fail(e)
	}
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		return fail(e)
	}
	if e = os.Chmod(path, 0600); e != nil {
		listener.Close()
		return fail(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{listener: listener, lock: lock, stop: cancel, done: make(chan struct{}), connections: map[*net.UnixConn]bool{}}
	go func() {
		defer close(s.done)
		var workers sync.WaitGroup
		defer workers.Wait()
		slots := make(chan struct{}, 8)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				conn.Close()
				continue
			}
			workers.Add(1)
			s.mu.Lock()
			if ctx.Err() != nil {
				s.mu.Unlock()
				conn.Close()
				workers.Done()
				return
			}
			s.connections[conn] = true
			s.mu.Unlock()
			go func() {
				defer workers.Done()
				defer func() { <-slots }()
				defer conn.Close()
				defer func() { s.mu.Lock(); delete(s.connections, conn); s.mu.Unlock() }()
				if checkPeer(conn) != nil {
					return
				}
				_ = conn.SetDeadline(time.Now().Add(70 * time.Second))
				var req Request
				if Read(conn, &req) != nil {
					return
				}
				var response Response
				if err := Validate(req); err != nil {
					response = Failure(err.Error())
				} else {
					callCtx, stop := context.WithTimeout(ctx, 60*time.Second)
					response = handle(callCtx, req)
					stop()
				}
				_ = Write(conn, response)
			}()
		}
	}()
	return s, nil
}
func (s *Server) Close() {
	s.once.Do(func() {
		s.stop()
		s.listener.Close()
		s.mu.Lock()
		for conn := range s.connections {
			conn.Close()
		}
		s.mu.Unlock()
		<-s.done
		s.lock.Close()
	})
}
func Call(ctx context.Context, r Request) (Response, error) {
	path, e := SocketPath()
	if e != nil {
		return Response{}, e
	}
	d := net.Dialer{}
	c, e := d.DialContext(ctx, "unix", path)
	if e != nil {
		return Response{}, e
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	conn := c.(*net.UnixConn)
	if e = checkPeer(conn); e != nil {
		return Response{}, e
	}
	deadline := time.Now().Add(70 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	if e = Write(c, r); e != nil {
		return Response{}, e
	}
	var response Response
	e = Read(c, &response)
	return response, e
}
func DesktopRunning() bool {
	dir, e := ConfigDir()
	if e != nil {
		return true
	}
	f, e := os.OpenFile(filepath.Join(dir, "desktop-history.json.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return true
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
