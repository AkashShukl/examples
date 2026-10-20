package main

import (
	"bufio"
	"reflect"
	"strings"
	"testing"
)

func TestRESPDecoder(t *testing.T) {
	cases := []struct {
		input string
		want any
	}{
		{"+PONG\r\n", "PONG"},
		{":42\r\n", int64(42)},
		{"$3\r\nfoo\r\n", "foo"},
		{"$-1\r\n", nil},
		{"*2\r\n:1\r\n$3\r\nbar\r\n", []any{int64(1), "bar"}},
	}
	for _, tc := range cases {
		got, err := readRESP(bufio.NewReader(strings.NewReader(tc.input)))
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("readRESP(%q) = %#v, %v; want %#v", tc.input, got, err, tc.want)
		}
	}
}

func TestRESPDecoderRedisError(t *testing.T) {
	_, err := readRESP(bufio.NewReader(strings.NewReader("-ERR example\r\n")))
	if err == nil { t.Fatal("expected Redis error") }
}
