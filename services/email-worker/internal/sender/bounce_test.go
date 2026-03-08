package sender

import (
	"errors"
	"net/textproto"
	"testing"
)

func TestClassifySMTPError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType BounceType
		wantCode int
	}{
		{name: "hard via textproto", err: &textproto.Error{Code: 550, Msg: "mailbox unavailable"}, wantType: BounceTypeHard, wantCode: 550},
		{name: "soft via message", err: errors.New("451 temporary local problem"), wantType: BounceTypeSoft, wantCode: 451},
		{name: "not smtp", err: errors.New("connection reset by peer"), wantType: BounceTypeNone, wantCode: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifySMTPError(tt.err)
			if got.Type != tt.wantType || got.SMTPCode != tt.wantCode {
				t.Fatalf("classification=%+v want type=%q code=%d", got, tt.wantType, tt.wantCode)
			}
		})
	}
}
