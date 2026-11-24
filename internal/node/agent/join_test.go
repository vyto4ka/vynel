package agent

import (
	"context"
	"crypto/x509"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFormatJoinError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "x509 certificate not yet valid",
			err:  status.Error(codes.Unavailable, `connection error: desc = "transport: authentication handshake failed: x509: certificate has expired or is not yet valid: current time 2026-11-22T10:40:22Z is before 2026-11-24T03:00:29Z"`),
			want: "системные часы",
		},
		{
			name: "typed x509 expired error",
			err:  x509.CertificateInvalidError{Reason: x509.Expired},
			want: "timedatectl",
		},
		{
			name: "token rejected",
			err:  status.Error(codes.PermissionDenied, "token expired"),
			want: "панель отклонила токен",
		},
		{
			name: "wrong CA",
			err:  errors.New("panel certificate is not signed by the pinned CA"),
			want: "отвечает другой сервер или панель",
		},
		{
			name: "connection refused",
			err:  status.Error(codes.Unavailable, "dial tcp 203.0.113.10:9443: connect: connection refused"),
			want: "не удалось подключиться к панели",
		},
		{
			name: "deadline exceeded",
			err:  context.DeadlineExceeded,
			want: "не удалось подключиться к панели",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatJoinError("203.0.113.10:9443", tt.err)
			if !strings.Contains(got.Error(), tt.want) {
				t.Fatalf("got %q, want substring %q", got.Error(), tt.want)
			}
			if !errors.Is(got, tt.err) {
				t.Fatalf("wrapped error lost original error: %v", got)
			}
		})
	}
}
