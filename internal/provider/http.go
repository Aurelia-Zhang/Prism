package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func defaultHTTPClient(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}

func endpoint(base, suffix string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", fmt.Errorf("base URL is required")
	}
	parsed, err := url.Parse(strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid base URL %q", base)
	}
	return parsed.String(), nil
}

func readHTTPError(response *http.Response) *Error {
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	message := strings.TrimSpace(string(body))
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		message = envelope.Error.Message
		if envelope.Error.Code != "" {
			message = envelope.Error.Code + ": " + message
		}
		if envelope.Error.Type != "" {
			message = envelope.Error.Type + ": " + message
		}
	}
	if readErr != nil {
		message = readErr.Error()
	}
	if message == "" {
		message = response.Status
	}
	err := mapHTTPError(response.StatusCode, message)
	err.RetryAfter = parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	return err
}

func mapHTTPError(status int, message string) *Error {
	code := "server_error"
	retryable := false
	switch {
	case status == http.StatusUnauthorized:
		code = "authentication"
	case status == http.StatusForbidden:
		code = "permission"
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		code = "invalid_request"
	case status == http.StatusRequestTimeout:
		code = "timeout"
		retryable = true
	case status == http.StatusTooManyRequests:
		code = "rate_limit"
		retryable = true
	case status == http.StatusRequestEntityTooLarge:
		code = "context_length"
	case status >= 500:
		retryable = true
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "context length") || strings.Contains(lower, "maximum context") || strings.Contains(lower, "max tokens") {
		code = "context_length"
		retryable = false
	} else if strings.Contains(lower, "content filter") || strings.Contains(lower, "content_filter") {
		code = "content_filter"
		retryable = false
	}
	return &Error{Code: code, Message: message, Retryable: retryable}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && seconds >= 0 {
		return time.Duration(seconds * float64(time.Second))
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func networkError(err error) *Error {
	if err == nil {
		return nil
	}
	structured := ErrorFrom(err, "network")
	if structured.Code == "context_canceled" || structured.Code == "context_deadline_exceeded" {
		return structured
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		structured.Code = "timeout"
	} else {
		structured.Code = "network"
	}
	structured.Retryable = true
	return structured
}
