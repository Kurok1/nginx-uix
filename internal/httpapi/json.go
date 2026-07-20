/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"
)

var (
	errUnsupportedJSONMediaType = errors.New("unsupported JSON media type")
	errRequestBodyTooLarge      = errors.New("request body too large")
)

func decodeStrictJSON[T any](request *http.Request, limit int64) (T, error) {
	var zero T
	if request == nil || request.Body == nil || limit < 1 {
		return zero, fmt.Errorf("decode JSON request: invalid input")
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return zero, errUnsupportedJSONMediaType
	}
	payload, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil {
		return zero, fmt.Errorf("read JSON request: %w", err)
	}
	if int64(len(payload)) > limit {
		return zero, errRequestBodyTooLarge
	}
	if len(payload) == 0 || !utf8.Valid(payload) {
		return zero, fmt.Errorf("decode JSON request: invalid UTF-8 or empty body")
	}
	if err := validateUniqueJSONObject(payload, exactJSONFields(reflect.TypeFor[T]())); err != nil {
		return zero, err
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, fmt.Errorf("decode JSON request: %w", err)
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return zero, fmt.Errorf("decode JSON request: trailing data")
	}
	return value, nil
}

func validateUniqueJSONObject(payload []byte, exactFields map[string]struct{}) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("scan JSON request: %w", err)
	}
	opening, ok := token.(json.Delim)
	if !ok || opening != '{' {
		return fmt.Errorf("scan JSON request: object required")
	}
	if err := scanJSONObject(decoder, exactFields); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return fmt.Errorf("scan JSON request: trailing data")
	}
	return nil
}

func scanJSONObject(decoder *json.Decoder, exactFields map[string]struct{}) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("scan JSON object key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("scan JSON object: string key required")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("scan JSON object: duplicate field")
		}
		if exactFields != nil {
			if _, allowed := exactFields[key]; !allowed {
				return fmt.Errorf("scan JSON object: unknown field")
			}
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return fmt.Errorf("scan JSON object: invalid closing delimiter")
	}
	return nil
}

func exactJSONFields(valueType reflect.Type) map[string]struct{} {
	for valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	if valueType.Kind() != reflect.Struct {
		return nil
	}

	fields := make(map[string]struct{}, valueType.NumField())
	for index := range valueType.NumField() {
		field := valueType.Field(index)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = struct{}{}
	}
	return fields
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("scan JSON value: %w", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return scanJSONObject(decoder, nil)
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("scan JSON array: invalid closing delimiter")
		}
		return nil
	default:
		return fmt.Errorf("scan JSON value: unexpected delimiter")
	}
}
