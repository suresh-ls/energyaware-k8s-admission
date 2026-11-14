// Package logging supplies structured logging for the standalone Kepler tools.
package logging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
)

// New writes JSON logs to the supplied diagnostic stream, separate from data.
func New(out io.Writer, level string) (*slog.Logger, error) {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level must be debug, info, warn or error")
	}
	return slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: parsed})), nil
}

// OrDefault permits callers to inject their own logger without changing globals.
func OrDefault(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.Default()
	}
	return logger
}

// ErrorKind intentionally excludes raw error text. Transport errors can include
// credential-bearing URLs; parse errors can include provider response content.
func ErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	var transport *url.Error
	if errors.As(err, &transport) {
		return "transport"
	}
	var syntax *json.SyntaxError
	var value *json.UnmarshalTypeError
	if errors.As(err, &syntax) || errors.As(err, &value) {
		return "invalid_response"
	}
	return "operation_failed"
}
