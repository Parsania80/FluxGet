//go:build windows

package browserbridge

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/Microsoft/go-winio"
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"golang.org/x/sys/windows"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func pipeName() (string, string, error) {
	sid, err := winplatform.UserSID()
	if err != nil {
		return "", "", err
	}
	digest := sha256.Sum256([]byte(sid))
	return fmt.Sprintf(`\\.\pipe\FluxGet.Browser.v1.%x`, digest[:16]), sid, nil
}
func checkPipePeer(conn net.Conn, server bool, sid string) error {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("cannot verify pipe peer")
	}
	var pid uint32
	var err error
	if server {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(file.Fd()), &pid)
	} else {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid)
	}
	if err != nil {
		return err
	}
	peer, err := winplatform.ProcessSID(pid)
	if err != nil {
		return err
	}
	if peer != sid {
		return errors.New("browser IPC peer is not the current user")
	}
	return nil
}

type Server struct {
	mu          sync.Mutex
	connections map[net.Conn]bool
	listener    net.Listener
	stop        context.CancelFunc
	done        chan struct{}
	once        sync.Once
}

func Serve(handle func(context.Context, Request) Response) (*Server, error) {
	name, sid, err := pipeName()
	if err != nil {
		return nil, err
	}
	// The protected DACL excludes other accounts; go-winio also rejects remote
	// clients and atomically creates the first pipe instance for exclusive ownership.
	listener, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")", InputBufferSize: MaximumMessage + 4, OutputBufferSize: MaximumMessage + 4})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{listener: listener, stop: cancel, done: make(chan struct{}), connections: map[net.Conn]bool{}}
	go func() {
		defer close(s.done)
		var workers sync.WaitGroup
		defer workers.Wait()
		slots := make(chan struct{}, 8)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			select {
			case slots <- struct{}{}:
			default:
				conn.Close()
				continue
			}
			s.mu.Lock()
			if ctx.Err() != nil {
				s.mu.Unlock()
				conn.Close()
				<-slots
				return
			}
			s.connections[conn] = true
			s.mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { <-slots }()
				defer func() { conn.Close(); s.mu.Lock(); delete(s.connections, conn); s.mu.Unlock() }()
				if checkPipePeer(conn, true, sid) != nil {
					return
				}
				_ = conn.SetDeadline(time.Now().Add(70 * time.Second))
				var req Request
				if Read(conn, &req) != nil {
					return
				}
				var reply Response
				if err := Validate(req); err != nil {
					reply = Failure(err.Error())
				} else {
					callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
					reply = handle(callCtx, req)
					cancel()
				}
				_ = Write(conn, reply)
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
		for c := range s.connections {
			c.Close()
		}
		s.mu.Unlock()
		<-s.done
	})
}
func Call(ctx context.Context, req Request) (Response, error) {
	name, sid, err := pipeName()
	if err != nil {
		return Response{}, err
	}
	conn, err := winio.DialPipeContext(ctx, name)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	if err = checkPipePeer(conn, false, sid); err != nil {
		return Response{}, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline := time.Now().Add(70 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if err = Write(conn, req); err != nil {
		return Response{}, err
	}
	var reply Response
	err = Read(conn, &reply)
	return reply, err
}
func DesktopRunning() bool {
	dir, err := ConfigDir()
	if err != nil {
		return true
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return true
	}
	f, err := winplatform.OpenLocked(filepath.Join(dir, "desktop-history.json.lock"), true)
	if err != nil {
		return true
	}
	f.Close()
	return false
}
