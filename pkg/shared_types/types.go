// shared_types/types.go
package shared_types

import "time"

// Topic Constants - used by both client and server for routing.
const (
	TopicGetTime           = "system:get_time"
	TopicServerAnnounce    = "server:announcements"
	TopicClientGetStatus   = "client:get_status" // Server requests this from client
	TopicUserDetails       = "user:get_details"
	TopicErrorTest         = "system:error_test"
	TopicSlowClientRequest = "client:slow_request" // Server sends to client, client is slow
	TopicSlowServerRequest = "server:slow_request" // Client sends to server, server is slow
	TopicBroadcastTest     = "test:broadcast"
	TopicClientRegister    = "system:register" // Client registration
)

// --- Message Structs ---
// These structs define the expected JSON payloads for messages.
// They are shared between client and server to ensure type consistency.

// GetTimeRequest is used when client requests server time. (No payload fields)
type GetTimeRequest struct{}

// GetTimeResponse is the server's response with the current time.
type GetTimeResponse struct {
	CurrentTime string `json:"currentTime"`
}

// ServerAnnouncement is a message pushed by the server.
type ServerAnnouncement struct {
	Message   string `json:"message"`
	Timestamp string `json:"timestamp"`
}

// GetUserDetailsRequest is used by client to request user details.
type GetUserDetailsRequest struct {
	UserID string `json:"userId"`
}

// UserDetailsResponse contains user details sent by the server.
type UserDetailsResponse struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email"`
}

// ClientStatusQuery is used by server to request client's status.
type ClientStatusQuery struct {
	QueryDetailLevel string `json:"queryDetailLevel,omitempty"`
}

// ClientStatusReport is the client's response to a status query.
type ClientStatusReport struct {
	ClientID string `json:"clientId"`
	Status   string `json:"status"`
	Uptime   string `json:"uptime"`
}

// ErrorTestRequest is for testing error propagation.
type ErrorTestRequest struct {
	ShouldError bool `json:"shouldError"`
}

// ErrorTestResponse is the response for error tests.
type ErrorTestResponse struct {
	Message string `json:"message"`
}

// SlowClientRequest is sent by server to test client's slow response handling.
type SlowClientRequest struct {
	DelayMilliseconds int `json:"delayMilliseconds"`
}

// SlowClientResponse is client's response after a delay.
type SlowClientResponse struct {
	Message string `json:"message"`
}

// SlowServerRequest is sent by client to test server's slow response handling.
type SlowServerRequest struct {
	DelayMilliseconds int `json:"delayMilliseconds"`
}

// SlowServerResponse is server's response after a delay.
type SlowServerResponse struct {
	Message string `json:"message"`
}

// BroadcastMessage is used for testing publish to multiple clients.
type BroadcastMessage struct {
	Content string    `json:"content"`
	SentAt  time.Time `json:"sentAt"`
}

// ClientRegistration is sent by the client during connection to provide identity information
type ClientRegistration struct {
	ClientID   string `json:"clientId"`   // Client-generated ID
	ClientName string `json:"clientName"` // Human-readable name for the client
	ClientType string `json:"clientType"` // Type of client (e.g., "browser", "app", "service")
	ClientURL  string `json:"clientUrl"`  // URL of the client (for browser connections)
}

// ClientRegistrationResponse is sent by the server to confirm client registration
type ClientRegistrationResponse struct {
	ServerAssignedID string `json:"serverAssignedId"` // Server-generated ID that becomes the source of truth
	ClientName       string `json:"clientName"`       // Confirmed or modified client name
	ServerTime       string `json:"serverTime"`       // Server time for synchronization
}
