package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// A minimal RESP2 client for this reproducible lab, not production use.
type redisClient struct{ addr string }

func (c redisClient) Do(ctx context.Context, args ...string) (any, error) {
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("connect to Redis at %s: %w", c.addr, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, err
	}
	out := bufio.NewWriter(conn)
	if _, err := fmt.Fprintf(out, "*%d\r\n", len(args)); err != nil {
		return nil, err
	}
	for _, arg := range args {
		if _, err := fmt.Fprintf(out, "$%d\r\n%s\r\n", len(arg), arg); err != nil {
			return nil, err
		}
	}
	if err := out.Flush(); err != nil { return nil, err }
	return readRESP(bufio.NewReader(conn))
}

func readRESP(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil { return nil, err }
	readLine := func() (string, error) {
		line, err := r.ReadString('\n')
		if err != nil { return "", err }
		if !strings.HasSuffix(line, "\r\n") { return "", errors.New("malformed RESP line") }
		return strings.TrimSuffix(line, "\r\n"), nil
	}
	line, err := readLine()
	if err != nil { return nil, err }
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, fmt.Errorf("redis error: %s", line)
	case ':':
		return strconv.ParseInt(line, 10, 64)
	case '$':
		n, err := strconv.Atoi(line)
		if err != nil { return nil, err }
		if n == -1 { return nil, nil }
		if n < -1 || n > 16*1024*1024 { return nil, fmt.Errorf("invalid bulk length: %d", n) }
		payload := make([]byte, n+2)
		if _, err := io.ReadFull(r, payload); err != nil { return nil, err }
		if string(payload[n:]) != "\r\n" { return nil, errors.New("missing bulk terminator") }
		return string(payload[:n]), nil
	case '*':
		n, err := strconv.Atoi(line)
		if err != nil { return nil, err }
		if n == -1 { return nil, nil }
		if n < -1 || n > 1000 { return nil, fmt.Errorf("invalid array length: %d", n) }
		items := make([]any, 0, n)
		for i := 0; i < n; i++ {
			item, err := readRESP(r)
			if err != nil { return nil, err }
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported RESP prefix %q", prefix)
	}
}
