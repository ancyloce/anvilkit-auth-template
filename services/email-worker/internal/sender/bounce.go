package sender

import (
	"errors"
	"net/textproto"
	"regexp"
	"strconv"
)

type BounceType string

const (
	BounceTypeNone BounceType = ""
	BounceTypeSoft BounceType = "soft"
	BounceTypeHard BounceType = "hard"
)

type BounceClassification struct {
	Type     BounceType
	SMTPCode int
}

var smtpCodeRegexp = regexp.MustCompile(`\b([45][0-9]{2})\b`)

func ClassifySMTPError(err error) BounceClassification {
	if err == nil {
		return BounceClassification{}
	}

	var protoErr *textproto.Error
	if errors.As(err, &protoErr) {
		return classifySMTPCode(protoErr.Code)
	}

	matches := smtpCodeRegexp.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return BounceClassification{}
	}

	code, convErr := strconv.Atoi(matches[1])
	if convErr != nil {
		return BounceClassification{}
	}

	return classifySMTPCode(code)
}

func classifySMTPCode(code int) BounceClassification {
	classification := BounceClassification{SMTPCode: code}
	switch {
	case code >= 400 && code < 500:
		classification.Type = BounceTypeSoft
	case code >= 500 && code < 600:
		classification.Type = BounceTypeHard
	default:
		classification.Type = BounceTypeNone
	}
	return classification
}
