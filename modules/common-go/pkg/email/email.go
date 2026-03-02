package email

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
)

var (
	ErrEmptySMTPHost   = errors.New("empty_smtp_host")
	ErrInvalidSMTPPort = errors.New("invalid_smtp_port")
	ErrEmptyFromEmail  = errors.New("empty_from_email")
	ErrInvalidToEmail  = errors.New("invalid_to_email")
	ErrEmptyBody       = errors.New("empty_body")
	ErrInvalidHeader   = errors.New("invalid_header")
)

var smtpSendMail = smtp.SendMail

// SMTPConfig stores SMTP delivery settings and default sender identity.
type SMTPConfig struct {
	Host      string
	Port      int
	Username  string
	Password  string
	FromEmail string
	FromName  string
}

// SendEmail sends a single multipart/alternative message to one recipient.
func (c SMTPConfig) SendEmail(to, subject, htmlBody, textBody string) error {
	if err := c.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(htmlBody) == "" && strings.TrimSpace(textBody) == "" {
		return ErrEmptyBody
	}
	if hasHeaderBreak(subject) || hasHeaderBreak(c.FromName) {
		return ErrInvalidHeader
	}

	toAddr, err := mail.ParseAddress(strings.TrimSpace(to))
	if err != nil || toAddr.Address == "" {
		return ErrInvalidToEmail
	}

	fromHeader := (&mail.Address{
		Name:    strings.TrimSpace(c.FromName),
		Address: strings.TrimSpace(c.FromEmail),
	}).String()

	msg, err := buildMessage(fromHeader, toAddr.String(), subject, htmlBody, textBody)
	if err != nil {
		return err
	}

	var auth smtp.Auth
	if strings.TrimSpace(c.Username) != "" || c.Password != "" {
		auth = smtp.PlainAuth("", c.Username, c.Password, c.Host)
	}

	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	return smtpSendMail(addr, auth, c.FromEmail, []string{toAddr.Address}, msg)
}

// GenerateOTP returns a zero-padded 6-digit numeric code in range 000000-999999.
func GenerateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// GenerateMagicToken returns a 32-byte random token encoded as URL-safe base64.
func GenerateMagicToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken hashes token with SHA256 and returns lowercase hex encoding.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (c SMTPConfig) validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return ErrEmptySMTPHost
	}
	if c.Port <= 0 || c.Port > 65535 {
		return ErrInvalidSMTPPort
	}
	if strings.TrimSpace(c.FromEmail) == "" {
		return ErrEmptyFromEmail
	}
	fromAddr, err := mail.ParseAddress(strings.TrimSpace(c.FromEmail))
	if err != nil || fromAddr.Address == "" {
		return ErrEmptyFromEmail
	}
	return nil
}

func buildMessage(fromHeader, toHeader, subject, htmlBody, textBody string) ([]byte, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	var msg bytes.Buffer
	msg.WriteString("From: " + fromHeader + "\r\n")
	msg.WriteString("To: " + toHeader + "\r\n")
	msg.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=%q\r\n", writer.Boundary()))
	msg.WriteString("\r\n")

	if strings.TrimSpace(textBody) != "" {
		if err := writePart(writer, "text/plain; charset=UTF-8", textBody); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(htmlBody) != "" {
		if err := writePart(writer, "text/html; charset=UTF-8", htmlBody); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}
	msg.Write(body.Bytes())
	return msg.Bytes(), nil
}

func writePart(writer *multipart.Writer, contentType, body string) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	header.Set("Content-Transfer-Encoding", "quoted-printable")

	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(part)
	if _, err := qp.Write([]byte(body)); err != nil {
		_ = qp.Close()
		return err
	}
	return qp.Close()
}

func hasHeaderBreak(v string) bool {
	return strings.ContainsAny(v, "\r\n")
}
