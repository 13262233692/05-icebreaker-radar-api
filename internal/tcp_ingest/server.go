// Package tcp_ingest accepts raw binary radar echo streams from radar front
// ends over TCP and hands decoded frames to the processing pipeline.
package tcp_ingest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/icebreaker/ice-radar-api/internal/echo_parser"
	"github.com/icebreaker/ice-radar-api/internal/model"
)

// FrameSink consumes one decoded echo frame.
type FrameSink interface {
	Ingest(ctx context.Context, frame model.EchoFrame) bool
}

// Server listens for TCP connections from radar front ends.
type Server struct {
	addr   string
	sink   FrameSink
	logger *slog.Logger

	listener net.Listener
	wg       sync.WaitGroup

	mu         sync.Mutex
	accepted   int64
	framesOK   int64
	framesBad  int64
	framesDrop int64
}

func New(addr string, sink FrameSink, logger *slog.Logger) *Server {
	return &Server{addr: addr, sink: sink, logger: logger}
}

// Stats counters exposed for observability.
type Stats struct {
	Connections int64 `json:"connections"`
	FramesOK    int64 `json:"frames_ok"`
	FramesBad   int64 `json:"frames_bad"`
	FramesDrop  int64 `json:"frames_dropped"`
}

func (s *Server) StatsSnapshot() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Connections: s.accepted,
		FramesOK:    s.framesOK,
		FramesBad:   s.framesBad,
		FramesDrop:  s.framesDrop,
	}
}

func (s *Server) add(delta *int64, n int64) {
	s.mu.Lock()
	*delta += n
	s.mu.Unlock()
}

// Start begins accepting connections. It blocks until the listener closes.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("tcp_ingest: listen %s: %w", s.addr, err)
	}
	s.listener = ln
	s.logger.Info("tcp ingest listening", "addr", s.addr)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("tcp_ingest: accept: %w", err)
		}
		s.add(&s.accepted, 1)
		s.wg.Add(1)
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	s.logger.Info("radar front end connected", "remote", remote)

	// A single buffered reader is reused so bytes read ahead while parsing
	// one frame are not lost before parsing the next.
	br := bufio.NewReaderSize(conn, echo_parser.HeaderLen+echo_parser.MaxBins+echo_parser.CRCLen)

	for {
		frame, err := echo_parser.ReadFrame(br)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				s.logger.Info("radar front end disconnected", "remote", remote)
				return
			}
			s.add(&s.framesBad, 1)
			s.logger.Warn("bad frame, resynchronizing",
				"remote", remote, "error", err.Error())
			continue
		}

		if !s.sink.Ingest(ctx, *frame) {
			s.add(&s.framesDrop, 1)
		} else {
			s.add(&s.framesOK, 1)
		}
	}
}

// Close waits for all connection handlers to finish.
func (s *Server) Close() {
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
}
