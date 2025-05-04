// pkg/model/message.go
package model

import "time"

type MessageHeader struct {
    MessageID     string `json:"messageID"`
    CorrelationID string `json:"correlationID,omitempty"`
    Type          string `json:"type"`
    Topic         string `json:"topic"`
    Timestamp     int64  `json:"timestamp"`
    TTL           int64  `json:"ttl,omitempty"`
}

type Message struct {
    Header MessageHeader `json:"header"`
    Body   any           `json:"body"`
}

func NewEvent(topic string, body any) *Message {
    return &Message{
        Header: MessageHeader{
            MessageID: randomID(),
            Type:      "event",
            Topic:     topic,
            Timestamp: time.Now().UnixMilli(),
        },
        Body: body,
    }
}

// TODO: replace with UUID v4 – keep zero‑dep for now.
func randomID() string { return time.Now().Format("150405.000000") }
