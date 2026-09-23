// SPDX-License-Identifier: Apache-2.0

























package delivery

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)



const purpose = "baton-delivery-v1"




const canonicalLines = 15








var ErrFieldNewline = errors.New("delivery: a field contains a newline")



type Envelope struct {
	MessageID string





	Sender    string
	Recipient string



	SourceNetwork      string
	DestinationNetwork string



	CreatedAt time.Time


	ExpiresAt time.Time

	Type        string
	ContentType string



	PayloadSize int

	ThreadID string
	ReplyTo  string




	Attachments []spi.Attachment
}



type Delivery struct {
	Envelope  Envelope
	Payload   []byte
	Signature []byte
}






func Canonical(e Envelope, payload []byte) ([]byte, error) {
	sum := sha256.Sum256(payload)
	return CanonicalWithDigest(e, hex.EncodeToString(sum[:]))
}










func CanonicalWithDigest(e Envelope, payloadSHA256 string) ([]byte, error) {
	ad, err := attachmentsDigest(e.Attachments)
	if err != nil {
		return nil, err
	}
	fields := []string{
		purpose,
		e.MessageID,
		e.Sender,
		e.Recipient,
		e.SourceNetwork,
		e.DestinationNetwork,
		stamp(e.CreatedAt),
		stamp(e.ExpiresAt),
		e.Type,
		e.ContentType,
		strconv.Itoa(e.PayloadSize),
		e.ThreadID,
		e.ReplyTo,
		ad,
		payloadSHA256,
	}
	if len(fields) != canonicalLines {
		return nil, fmt.Errorf("delivery: built %d lines, this build signs %d", len(fields), canonicalLines)
	}
	for i, f := range fields {
		if strings.Contains(f, "\n") {
			return nil, fmt.Errorf("%w: line %d", ErrFieldNewline, i)
		}
	}
	return []byte(strings.Join(fields, "\n")), nil
}



func Sign(priv ed25519.PrivateKey, e Envelope, payload []byte) ([]byte, error) {
	c, err := Canonical(e, payload)
	if err != nil {
		return nil, err
	}
	return ed25519.Sign(priv, c), nil
}








func Verify(pub ed25519.PublicKey, d Delivery) bool {
	if len(d.Signature) == 0 {
		return false
	}
	c, err := Canonical(d.Envelope, d.Payload)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, c, d.Signature)
}







func attachmentsDigest(as []spi.Attachment) (string, error) {
	if len(as) == 0 {
		return "", nil
	}
	lines := make([]string, 0, len(as)*5)
	for _, a := range as {
		lines = append(lines,
			strconv.Itoa(a.Index),
			a.Name,
			a.ContentType,
			strconv.FormatInt(a.Size, 10),
			a.SHA256,
		)
	}
	for i, l := range lines {
		if strings.Contains(l, "\n") {
			return "", fmt.Errorf("%w: attachment field %d", ErrFieldNewline, i)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
