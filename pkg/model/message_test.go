package model

import (
	"testing"
	"time"
)

func TestNewEvent(t *testing.T) {
	// Test with a string topic and map body
	t.Run("String topic and map body", func(t *testing.T) {
		topic := "test.topic"
		body := map[string]any{
			"key": "value",
		}

		msg := NewEvent(topic, body)

		if msg == nil {
			t.Fatal("Expected message to be created, got nil")
		}

		if msg.Header.Topic != topic {
			t.Fatalf("Expected topic %s, got %s", topic, msg.Header.Topic)
		}

		if msg.Header.Type != "event" {
			t.Fatalf("Expected type %s, got %s", "event", msg.Header.Type)
		}

		if msg.Header.MessageID == "" {
			t.Fatal("Expected message ID to be set, got empty string")
		}

		if msg.Header.Timestamp == 0 {
			t.Fatal("Expected timestamp to be set, got 0")
		}

		if msg.Body == nil {
			t.Fatal("Expected body to be set, got nil")
		}
	})

	// Test with an empty topic
	t.Run("Empty topic", func(t *testing.T) {
		topic := ""
		body := "test body"

		msg := NewEvent(topic, body)

		if msg == nil {
			t.Fatal("Expected message to be created, got nil")
		}

		if msg.Header.Topic != topic {
			t.Fatalf("Expected topic %s, got %s", topic, msg.Header.Topic)
		}
	})

	// Test with a nil body
	t.Run("Nil body", func(t *testing.T) {
		topic := "test.topic"
		var body any = nil

		msg := NewEvent(topic, body)

		if msg == nil {
			t.Fatal("Expected message to be created, got nil")
		}

		if msg.Body != nil {
			t.Fatalf("Expected body to be nil, got %v", msg.Body)
		}
	})

	// Test with a complex body
	t.Run("Complex body", func(t *testing.T) {
		topic := "test.topic"
		body := struct {
			Name    string
			Age     int
			IsAdmin bool
		}{
			Name:    "John",
			Age:     30,
			IsAdmin: true,
		}

		msg := NewEvent(topic, body)

		if msg == nil {
			t.Fatal("Expected message to be created, got nil")
		}

		if msg.Body == nil {
			t.Fatal("Expected body to be set, got nil")
		}
	})
}

func TestRandomID(t *testing.T) {
	// Generate a few IDs and check the format
	// Note: We can't guarantee uniqueness in a fast-running test
	// since the implementation uses time with microsecond precision
	for i := 0; i < 5; i++ {
		id := randomID()

		// Ensure the ID is not empty
		if id == "" {
			t.Fatal("Generated empty ID")
		}

		// Ensure the ID format is as expected (based on time.Now().Format("150405.000000"))
		// or similar time-based format
		if len(id) < 10 {
			t.Fatalf("ID format is not as expected: %s (too short)", id)
		}

		// Sleep a bit to ensure different timestamps
		time.Sleep(1 * time.Millisecond)
	}
}
