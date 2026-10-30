package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
)

type Request struct {
	Command string `json:"command"`
	Target  string `json:"target"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	Data  string `json:"data"`
}

type Handler func(uid uint32, req Request) Response

// SocketPath returns the admin socket location for this platform.
func SocketPath() string {
	return socketPath()
}

// Call sends one request over conn and returns the response.
func Call(conn net.Conn, req Request) (Response, error) {
	data, err := json.Marshal(req)
	if err != nil {
		return Response{}, fmt.Errorf("failed to encode request: %w", err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return Response{}, fmt.Errorf("failed to send request: %w", err)
	}
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return Response{}, fmt.Errorf("failed to read response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, fmt.Errorf("failed to decode response: %w", err)
	}
	return resp, nil
}

// Serve accepts connections on ln and answers each request with handler.
func Serve(ln net.Listener, handler Handler) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go serveConn(conn, handler)
	}
}

func serveConn(conn net.Conn, handler Handler) {
	defer conn.Close()
	uid := connUID(conn)
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeResponse(conn, Response{OK: false, Error: "invalid request"})
			continue
		}
		writeResponse(conn, handler(uid, req))
	}
}

func writeResponse(conn net.Conn, resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	conn.Write(append(data, '\n'))
}

// CommandRequest sends one command to the daemon at socketPath.
func CommandRequest(socketPath string, req Request) (Response, error) {
	conn, err := Dial(socketPath)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()
	return Call(conn, req)
}
