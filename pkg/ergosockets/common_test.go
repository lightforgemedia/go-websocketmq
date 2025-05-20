package ergosockets_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lightforgemedia/go-websocketmq/pkg/ergosockets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test structs
type TestValueStruct struct {
	Name  string `json:"name"`
	Value int    `json:"value"`
}

type TestPtrStruct struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type TestNestedStruct struct {
	Title   string         `json:"title"`
	Details *TestPtrStruct `json:"details,omitempty"`
}

func TestDecodeAndPrepareArg(t *testing.T) {
	t.Run("Decode value struct", func(t *testing.T) {
		// Test decoding into a non-pointer struct type
		targetType := reflect.TypeOf(TestValueStruct{})
		payload := []byte(`{"name":"test","value":42}`)

		result, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.NoError(t, err)
		require.Equal(t, reflect.Struct, result.Kind())
		assert.Equal(t, "test", result.FieldByName("Name").String())
		assert.Equal(t, 42, int(result.FieldByName("Value").Int()))
	})

	t.Run("Decode pointer struct", func(t *testing.T) {
		// Test decoding into a pointer struct type
		targetType := reflect.TypeOf(&TestPtrStruct{})
		payload := []byte(`{"id":"abc123","score":98.6}`)

		result, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.NoError(t, err)
		require.Equal(t, reflect.Ptr, result.Kind())
		require.True(t, result.IsValid())
		
		// Extract values from the pointer
		ptrValue := result.Interface().(*TestPtrStruct)
		assert.Equal(t, "abc123", ptrValue.ID)
		assert.Equal(t, 98.6, ptrValue.Score)
	})

	t.Run("Handle null payload with value type", func(t *testing.T) {
		// Test handling null payload with a value type
		targetType := reflect.TypeOf(TestValueStruct{})
		var payload json.RawMessage = []byte("null")

		result, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.NoError(t, err)
		require.Equal(t, reflect.Struct, result.Kind())
		// Should be a zero-value struct
		assert.Equal(t, "", result.FieldByName("Name").String())
		assert.Equal(t, 0, int(result.FieldByName("Value").Int()))
	})

	t.Run("Handle null payload with pointer type", func(t *testing.T) {
		// Test handling null payload with a pointer type
		targetType := reflect.TypeOf(&TestPtrStruct{})
		var payload json.RawMessage = []byte("null")

		result, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.NoError(t, err)
		require.Equal(t, reflect.Ptr, result.Kind())
		assert.True(t, result.IsNil()) // Should return nil pointer
	})

	t.Run("Handle nil payload with pointer type", func(t *testing.T) {
		// Test handling nil payload with a pointer type
		targetType := reflect.TypeOf(&TestPtrStruct{})
		var payload json.RawMessage = nil

		result, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.NoError(t, err)
		require.Equal(t, reflect.Ptr, result.Kind())
		assert.True(t, result.IsNil()) // Should return nil pointer
	})

	t.Run("Handle invalid JSON", func(t *testing.T) {
		// Test handling invalid JSON
		targetType := reflect.TypeOf(TestValueStruct{})
		payload := []byte(`{"name":"test",INVALID}`)

		_, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal payload")
	})

	t.Run("Handle nested struct", func(t *testing.T) {
		// Test handling nested struct
		targetType := reflect.TypeOf(TestNestedStruct{})
		payload := []byte(`{"title":"Test Title","details":{"id":"nested","score":75.5}}`)

		result, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.NoError(t, err)
		require.Equal(t, reflect.Struct, result.Kind())
		assert.Equal(t, "Test Title", result.FieldByName("Title").String())
		
		detailsField := result.FieldByName("Details")
		require.True(t, detailsField.IsValid())
		require.False(t, detailsField.IsNil())
		
		details := detailsField.Interface().(*TestPtrStruct)
		assert.Equal(t, "nested", details.ID)
		assert.Equal(t, 75.5, details.Score)
	})

	t.Run("Handle nested struct with null details", func(t *testing.T) {
		// Test handling nested struct with null field
		targetType := reflect.TypeOf(TestNestedStruct{})
		payload := []byte(`{"title":"Only Title","details":null}`)

		result, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.NoError(t, err)
		require.Equal(t, reflect.Struct, result.Kind())
		assert.Equal(t, "Only Title", result.FieldByName("Title").String())
		
		detailsField := result.FieldByName("Details")
		require.True(t, detailsField.IsValid())
		assert.True(t, detailsField.IsNil()) // Should be nil
	})

	t.Run("Handle type mismatch in JSON", func(t *testing.T) {
		// Test handling type mismatch
		targetType := reflect.TypeOf(TestValueStruct{})
		payload := []byte(`{"name":42,"value":"not-an-int"}`) // Types are wrong

		_, err := ergosockets.DecodeAndPrepareArg(payload, targetType)
		
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal payload")
	})
}