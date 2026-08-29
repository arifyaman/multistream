// Package daemonipc is a tiny request/response protocol between the
// multistream client commands and the running daemon. It carries no
// secrets: only "is the daemon up" and "please restart platform X".
//
// The transport is one length-prefixed JSON frame per direction over a
// Unix socket (Linux/macOS) or a Windows named pipe; each request uses its
// own connection. Go's net package serves both transports, so the framing
// code is transport-agnostic.
package daemonipc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	maxFrame = 1 << 20 // 1 MiB per frame
	timeout  = 5 * time.Second
)

// Request is one IPC request.
type Request struct {
	Op       string `json:"op"` // "ping" or "restart"
	Platform string `json:"platform,omitempty"`
}

// Response is one IPC response.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// RestartFunc is the supervisor's handler for a restart request.
type RestartFunc func(platform string) error

// Server accepts IPC requests and dispatches them.
type Server struct {
	network string
	address string
	restart RestartFunc
	ln      net.Listener
}

// NewServer builds a Server for the given network/address. It is not
// listening until Listen is called. restart may be nil (restart requests
// are then rejected).
func NewServer(network, address string, restart RestartFunc) *Server {
	return &Server{network: network, address: address, restart: restart}
}

// Addr returns the endpoint address clients dial.
func (s *Server) Addr() string { return s.address }

// Listen starts accepting connections. On Unix it first probes the endpoint:
// if a daemon is already listening it refuses to start (the single-instance
// guard); otherwise it clears any stale socket file and binds.
func (s *Server) Listen() error {
	if s.network == "unix" {
		if canDial(s.network, s.address) {
			return fmt.Errorf("endpoint %s already in use (another multistream daemon is running)", s.address)
		}
		os.Remove(s.address)
	}
	ln, err := net.Listen(s.network, s.address)
	if err != nil {
		return err
	}
	s.ln = ln
	go s.accept(ln)
	return nil
}

// canDial reports whether something is accepting connections on the endpoint.
func canDial(network, address string) bool {
	conn, err := net.DialTimeout(network, address, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (s *Server) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	var req Request
	if err := readFrame(conn, &req); err != nil {
		return
	}
	resp := Response{OK: true}
	switch req.Op {
	case "ping":
		// ok
	case "restart":
		if s.restart == nil {
			resp = Response{OK: false, Error: "restart not available"}
		} else if err := s.restart(req.Platform); err != nil {
			resp = Response{OK: false, Error: err.Error()}
		}
	default:
		resp = Response{OK: false, Error: "unknown op " + req.Op}
	}
	writeFrame(conn, resp)
}

// Close stops the listener and removes the endpoint file.
func (s *Server) Close() error {
	if s.ln == nil {
		return nil
	}
	err := s.ln.Close()
	s.ln = nil
	if s.network == "unix" {
		os.Remove(s.address)
	}
	return err
}

// Do sends one request and reads one response over a fresh connection.
func Do(network, address string, req Request) (Response, error) {
	conn, err := net.DialTimeout(network, address, timeout)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if err := writeFrame(conn, req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := readFrame(conn, &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

// Ping reports whether a daemon is listening on the endpoint.
func Ping(network, address string) error {
	resp, err := Do(network, address, Request{Op: "ping"})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

// Restart asks the daemon to restart one platform.
func Restart(network, address, platform string) error {
	resp, err := Do(network, address, Request{Op: "restart", Platform: platform})
	if err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	return nil
}

func writeFrame(w io.Writer, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > maxFrame {
		return fmt.Errorf("frame too large: %d bytes", len(data))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readFrame(r io.Reader, v interface{}) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxFrame {
		return fmt.Errorf("frame too large: %d bytes", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
