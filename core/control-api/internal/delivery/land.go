// SPDX-License-Identifier: Apache-2.0

package delivery

import (
	"errors"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)









type Local struct {
	TenantID  string
	NetworkID string



	VerifiedNetwork string











	SourceAddress string

	Now time.Time








	RetentionCap time.Duration
}

var (

	ErrUnverified = errors.New("delivery: no verified source network")



	ErrSourceNetworkMismatch = errors.New("delivery: source_network is not the network that signed")



	ErrNotForThisNetwork = errors.New("delivery: destination_network is not this network")

	ErrNoRetentionCap = errors.New("delivery: local retention cap is unset")

	ErrNoRecipient = errors.New("delivery: recipient is empty")

	ErrNoMessageID = errors.New("delivery: message_id is empty")
)












func Land(d Delivery, l Local) (spi.Message, error) {
	switch {
	case l.RetentionCap <= 0:
		return spi.Message{}, ErrNoRetentionCap
	case l.VerifiedNetwork == "":
		return spi.Message{}, ErrUnverified
	case d.Envelope.SourceNetwork != l.VerifiedNetwork:
		return spi.Message{}, ErrSourceNetworkMismatch
	case d.Envelope.DestinationNetwork != l.NetworkID:
		return spi.Message{}, ErrNotForThisNetwork
	case d.Envelope.MessageID == "":
		return spi.Message{}, ErrNoMessageID
	case d.Envelope.Recipient == "":
		return spi.Message{}, ErrNoRecipient
	}

	return spi.Message{
		MessageID: d.Envelope.MessageID,
		TenantID:  l.TenantID,



		SourceAgent:   d.Envelope.Sender,
		SourceNetwork: d.Envelope.SourceNetwork,



		SourceAddress: l.SourceAddress,

		DestinationAgent: d.Envelope.Recipient,




		DestinationNetwork: l.NetworkID,

		ThreadID: d.Envelope.ThreadID,
		ReplyTo:  d.Envelope.ReplyTo,

		CreatedAt: d.Envelope.CreatedAt,




		State: spi.MessageUnread,





		ExpiresAt: retention(d.Envelope.ExpiresAt, l),

		Type:        d.Envelope.Type,
		ContentType: d.Envelope.ContentType,



		PayloadSize: len(d.Payload),
		Payload:     d.Payload,
	}, nil
}







func retention(declared time.Time, l Local) time.Time {
	ceiling := l.Now.Add(l.RetentionCap)
	if declared.IsZero() || declared.After(ceiling) {
		return ceiling
	}
	return declared
}
