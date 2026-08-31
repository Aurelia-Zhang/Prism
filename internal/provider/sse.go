package provider

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

type sseEvent struct {
	Name string
	Data []byte
}

func readSSE(ctx context.Context, reader io.Reader, handle func(sseEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	var name string
	var data bytes.Buffer
	dispatch := func() error {
		if data.Len() == 0 && name == "" {
			return nil
		}
		event := sseEvent{Name: name, Data: append([]byte(nil), data.Bytes()...)}
		name = ""
		data.Reset()
		return handle(event)
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if ok {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			name = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := dispatch(); err != nil {
		return err
	}
	return nil
}

func streamProtocolError(message string) *Error {
	return &Error{Code: "stream_protocol", Message: fmt.Sprintf("%s", message)}
}
