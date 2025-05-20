// ergosockets/common.go
package ergosockets

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

// Cached error type for reflection.
var ErrType = reflect.TypeOf((*error)(nil)).Elem()

// GenerateID creates a new random hex string ID.
func GenerateID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("fallback-%x", reflect.ValueOf(TimeNow()).Int()) // TimeNow from time.go
	}
	return hex.EncodeToString(bytes)
}

// HandlerWrapper stores reflection info for invoking user-provided handlers.
type HandlerWrapper struct {
	HandlerFunc reflect.Value // The user's handler function
	ReqType     reflect.Type  // Type of the request payload struct (for request handlers)
	RespType    reflect.Type  // Type of the response payload struct (for request handlers that return one)
	MsgType     reflect.Type  // Type of the message payload (for subscription handlers)
}

// NewHandlerWrapper inspects the handler function and extracts type information.
// Supported signatures:
// Server HandleClientRequest: func(ClientHandle, ReqStruct) (RespStruct, error)
// Server HandleClientRequest: func(ClientHandle, ReqStruct) error
// Client Subscribe: func(MsgStruct) error
// Client HandleServerRequest: func(ReqStruct) (RespStruct, error)
// Client HandleServerRequest: func(ReqStruct) error
func NewHandlerWrapper(handlerFunc interface{}) (*HandlerWrapper, error) {
	fv := reflect.ValueOf(handlerFunc)
	ft := fv.Type()

	if ft.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler must be a function, got %T", handlerFunc)
	}

	hw := &HandlerWrapper{HandlerFunc: fv}
	numIn := ft.NumIn()
	numOut := ft.NumOut()

	// Check common error return (last output arg must be error)
	if numOut == 0 || !ft.Out(numOut-1).Implements(ErrType) {
		return nil, fmt.Errorf("handler must return an error as its last argument, signature: %s", ft.String())
	}

	// Client Subscribe: func(MsgStruct) error
	if numIn == 1 && numOut == 1 {
		hw.MsgType = ft.In(0)
		// Ensure MsgStruct is not an interface or pointer to interface, etc.
		// For simplicity, we assume it's a concrete type or pointer to struct.
		if hw.MsgType.Kind() == reflect.Interface || (hw.MsgType.Kind() == reflect.Ptr && hw.MsgType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("subscription handler message type cannot be an interface: %s", ft.String())
		}
		return hw, nil
	}

	// Client HandleServerRequest: func(ReqStruct) (RespStruct, error) or func(ReqStruct) error
	if numIn == 1 && (numOut == 1 || numOut == 2) {
		hw.ReqType = ft.In(0)
		if hw.ReqType.Kind() == reflect.Interface || (hw.ReqType.Kind() == reflect.Ptr && hw.ReqType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("client HandleServerRequest handler request type cannot be an interface: %s", ft.String())
		}
		if numOut == 2 { // func(ReqStruct) (RespStruct, error)
			hw.RespType = ft.Out(0)
			if hw.RespType.Kind() == reflect.Interface || (hw.RespType.Kind() == reflect.Ptr && hw.RespType.Elem().Kind() == reflect.Interface) {
				return nil, fmt.Errorf("client HandleServerRequest handler response type cannot be an interface: %s", ft.String())
			}
		}
		return hw, nil
	}

	// Server HandleClientRequest: func(ClientHandle, ReqStruct) (RespStruct, error) or func(ClientHandle, ReqStruct) error
	if numIn == 2 && (numOut == 1 || numOut == 2) {
		// First arg should be ClientHandle (interface) - we don't strictly check its type here
		// as it's passed by the broker itself.
		hw.ReqType = ft.In(1)
		if hw.ReqType.Kind() == reflect.Interface || (hw.ReqType.Kind() == reflect.Ptr && hw.ReqType.Elem().Kind() == reflect.Interface) {
			return nil, fmt.Errorf("server HandleClientRequest handler request type cannot be an interface: %s", ft.String())
		}
		if numOut == 2 { // func(ClientHandle, ReqStruct) (RespStruct, error)
			hw.RespType = ft.Out(0)
			if hw.RespType.Kind() == reflect.Interface || (hw.RespType.Kind() == reflect.Ptr && hw.RespType.Elem().Kind() == reflect.Interface) {
				return nil, fmt.Errorf("server HandleClientRequest handler response type cannot be an interface: %s", ft.String())
			}
		}
		return hw, nil
	}

	return nil, fmt.Errorf("unsupported handler signature: %s. Expected e.g., func(ch ClientHandle, req ReqT) (RespT, error), func(msg MsgT) error, or func(req ReqT) (RespT, error)", ft.String())
}

// TimeNow is a wrapper for time.Now, useful for testing if time needs to be mocked.
// For this implementation, we'll use the real time.Now().
var TimeNow = time.Now

// DecodeAndPrepareArg decodes a JSON payload into an instance of targetType
// and returns a reflect.Value suitable for calling a handler function.
// It handles whether targetType is a pointer or a value, and correctly handles
// special cases like null payloads.
func DecodeAndPrepareArg(payload json.RawMessage, targetType reflect.Type) (reflect.Value, error) {
	// Check if target type is nil (safety check)
	if targetType == nil {
		return reflect.Value{}, fmt.Errorf("targetType cannot be nil")
	}

	// Check if the payload is nil or "null" JSON
	isNullPayload := payload == nil || string(payload) == "null"

	// Special handling for null payloads with pointer types
	if isNullPayload && targetType.Kind() == reflect.Ptr {
		// For pointer receivers that expect nil on null payloads,
		// return the zero value of the target type (nil pointer)
		return reflect.Zero(targetType), nil
	}

	// Create a new instance to unmarshal into
	var instanceVal reflect.Value
	if targetType.Kind() == reflect.Ptr {
		// If it's a pointer type (*T), create a new pointer to the element type (T)
		instanceVal = reflect.New(targetType.Elem())
	} else {
		// If it's a value type (T), create a new pointer to it (*T)
		instanceVal = reflect.New(targetType)
	}

	// Only try to decode if the payload is not null
	if !isNullPayload {
		if err := json.Unmarshal(payload, instanceVal.Interface()); err != nil {
			return reflect.Value{}, fmt.Errorf("failed to unmarshal payload into %s: %w. Raw: %s", 
				targetType, err, string(payload))
		}
	}

	// Return the appropriate value for the handler function
	if targetType.Kind() == reflect.Ptr {
		// If handler expects a pointer (*T), return the pointer directly
		return instanceVal, nil
	} else {
		// If handler expects a value (T), dereference the pointer
		return instanceVal.Elem(), nil
	}
}
