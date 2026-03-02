package email

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestSMTPConfigSendEmail_BuildsMultipartAlternativeMessage(t *testing.T) {
	origSendMail := smtpSendMail
	t.Cleanup(func() { smtpSendMail = origSendMail })

	var gotAddr string
	var gotAuth smtp.Auth
	var gotFrom string
	var gotTo []string
	var gotMsg []byte
	smtpSendMail = func(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
		gotAddr = addr
		gotAuth = auth
		gotFrom = from
		gotTo = append([]string(nil), to...)
		gotMsg = append([]byte(nil), msg...)
		return nil
	}

	cfg := SMTPConfig{
		Host:      "smtp.example.com",
		Port:      587,
		Username:  "mailer",
		Password:  "secret",
		FromEmail: "noreply@example.com",
		FromName:  "AnvilKit",
	}

	err := cfg.SendEmail(
		"user@example.com",
		"Verify login",
		"<p>Your code is <b>123456</b></p>",
		"Your code is 123456",
	)
	if err != nil {
		t.Fatalf("send email: %v", err)
	}

	if gotAddr != "smtp.example.com:587" {
		t.Fatalf("smtp addr=%q want=%q", gotAddr, "smtp.example.com:587")
	}
	if gotAuth == nil {
		t.Fatal("expected auth to be configured")
	}
	if gotFrom != "noreply@example.com" {
		t.Fatalf("from=%q want=noreply@example.com", gotFrom)
	}
	if len(gotTo) != 1 || gotTo[0] != "user@example.com" {
		t.Fatalf("to=%v want=[user@example.com]", gotTo)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(gotMsg))
	if err != nil {
		t.Fatalf("read message: %v", err)
	}

	contentType := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	if mediaType != "multipart/alternative" {
		t.Fatalf("media type=%q want=multipart/alternative", mediaType)
	}

	boundary := params["boundary"]
	if boundary == "" {
		t.Fatal("expected multipart boundary")
	}

	reader := multipart.NewReader(msg.Body, boundary)
	parts := map[string]string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read multipart part: %v", err)
		}

		partMediaType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if err != nil {
			t.Fatalf("part content type: %v", err)
		}
		bodyBytes, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part body: %v", err)
		}
		parts[partMediaType] = string(bodyBytes)
	}

	if parts["text/plain"] != "Your code is 123456" {
		t.Fatalf("text part=%q want=%q", parts["text/plain"], "Your code is 123456")
	}
	if parts["text/html"] != "<p>Your code is <b>123456</b></p>" {
		t.Fatalf("html part=%q want=%q", parts["text/html"], "<p>Your code is <b>123456</b></p>")
	}
}

func TestSMTPConfigSendEmail_Validation(t *testing.T) {
	cfg := SMTPConfig{
		Host:      "smtp.example.com",
		Port:      587,
		FromEmail: "noreply@example.com",
	}

	if err := cfg.SendEmail("user@example.com", "subject", "", ""); !errors.Is(err, ErrEmptyBody) {
		t.Fatalf("err=%v want=%v", err, ErrEmptyBody)
	}

	cfg.Host = ""
	if err := cfg.SendEmail("user@example.com", "subject", "<p>x</p>", "x"); !errors.Is(err, ErrEmptySMTPHost) {
		t.Fatalf("err=%v want=%v", err, ErrEmptySMTPHost)
	}

	cfg.Host = "smtp.example.com"
	cfg.Port = 0
	if err := cfg.SendEmail("user@example.com", "subject", "<p>x</p>", "x"); !errors.Is(err, ErrInvalidSMTPPort) {
		t.Fatalf("err=%v want=%v", err, ErrInvalidSMTPPort)
	}

	cfg.Port = 587
	if err := cfg.SendEmail("invalid-email", "subject", "<p>x</p>", "x"); !errors.Is(err, ErrInvalidToEmail) {
		t.Fatalf("err=%v want=%v", err, ErrInvalidToEmail)
	}
}

func TestGenerateOTP_FormatAndRange(t *testing.T) {
	for i := 0; i < 1000; i++ {
		otp, err := GenerateOTP()
		if err != nil {
			t.Fatalf("generate otp: %v", err)
		}
		if len(otp) != 6 {
			t.Fatalf("otp len=%d want=6 otp=%q", len(otp), otp)
		}
		if !regexp.MustCompile(`^\d{6}$`).MatchString(otp) {
			t.Fatalf("otp=%q must be exactly 6 digits", otp)
		}

		n, err := strconv.Atoi(otp)
		if err != nil {
			t.Fatalf("atoi otp: %v", err)
		}
		if n < 0 || n > 999999 {
			t.Fatalf("otp int=%d out of range", n)
		}
	}
}

func TestGenerateMagicToken_URLSafe(t *testing.T) {
	token, err := GenerateMagicToken()
	if err != nil {
		t.Fatalf("generate magic token: %v", err)
	}

	if len(token) != 43 {
		t.Fatalf("token len=%d want=43 token=%q", len(token), token)
	}
	if strings.ContainsAny(token, "+/=") {
		t.Fatalf("token=%q contains non-url-safe characters", token)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(token) {
		t.Fatalf("token=%q includes unsupported characters", token)
	}
}

func TestHashToken_SHA256Hex(t *testing.T) {
	const token = "magic-token"
	const want = "75f16f3681a386b967e2879e4c266a781cce2e076e645d3ec3cc02df8c11be1e"

	got := HashToken(token)
	if got != want {
		t.Fatalf("hash=%q want=%q", got, want)
	}
}
