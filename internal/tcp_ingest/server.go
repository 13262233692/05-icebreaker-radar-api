// Package tcp_ingest 提供雷达回波 TCP 接入服务：
// 监听端口、管理连接、按协议拆包并将解析后的帧投递给下游处理器。
package tcp_ingest

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"icebreaker-radar/internal/echo_parser"
)

// FrameHandler 处理一条解析完成的回波帧。处理出错仅记录日志，不中断接收。
type FrameHandler func(ctx context.Context, frame *echo_parser.EchoFrame)

// Server 为 TCP 接入服务。
type Server struct {
	addr        string
	handler     FrameHandler
	readTimeout time.Duration

	mu     sync.Mutex
	ln     net.Listener
	conns  map[net.Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

func NewServer(addr string, handler FrameHandler) *Server {
	return &Server{
		addr:        addr,
		handler:     handler,
		readTimeout: 5 * time.Minute,
		conns:       make(map[net.Conn]struct{}),
	}
}

// Addr 返回实际监听地址（用于 :0 随机端口场景）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// Start 开始监听并接受连接，直到 ctx 取消或调用 Shutdown。
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	log.Printf("[tcp_ingest] listening on %s", ln.Addr())

	go func() {
		<-ctx.Done()
		_ = s.Shutdown()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				s.wg.Wait()
				return nil
			}
			log.Printf("[tcp_ingest] accept error: %v", err)
			continue
		}
		s.track(conn, true)
		s.wg.Add(1)
		go s.serveConn(ctx, conn)
	}
}

// Shutdown 关闭监听并断开所有活跃连接。
func (s *Server) Shutdown() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	return nil
}

func (s *Server) track(conn net.Conn, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.track(conn, false)
		_ = conn.Close()
	}()
	remote := conn.RemoteAddr().String()
	log.Printf("[tcp_ingest] radar frontend connected: %s", remote)
	defer log.Printf("[tcp_ingest] radar frontend disconnected: %s", remote)

	reader := echo_parser.NewStreamReader(conn)
	for {
		if s.readTimeout > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(s.readTimeout))
		}
		frame, err := reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			// 协议错误：记录并断开，避免错位后持续误解析
			log.Printf("[tcp_ingest] frame error from %s: %v", remote, err)
			return
		}
		s.handler(ctx, frame)
	}
}
