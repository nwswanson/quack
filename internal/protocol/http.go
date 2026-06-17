package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const ContentTypeJSON = "application/json"

type ErrorSetter interface {
	SetError(string)
}

type ErrorGetter interface {
	ErrorMessage() string
}

func NewRequest(ctx context.Context, method string, target string, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	AddBearerToken(req, token)
	return req, nil
}

func NewJSONRequest(ctx context.Context, method string, target string, token string, body any) (*http.Request, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}
	req, err := NewRequest(ctx, method, target, token, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", ContentTypeJSON)
	return req, nil
}

func AddBearerToken(req *http.Request, token string) {
	if token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

func DecodeResponse[T any](resp *http.Response) (T, error) {
	var out T
	body, err := ReadResponseBody(resp)
	if err != nil {
		return out, err
	}

	if err := json.Unmarshal(body, &out); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if setter, ok := any(&out).(ErrorSetter); ok {
				setter.SetError(FallbackResponseMessage(resp, body))
			}
			return out, nil
		}
		return out, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

func ReadResponseBody(resp *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return body, nil
}

func FallbackResponseMessage(resp *http.Response, body []byte) string {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	return message
}

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", ContentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, UploadArchiveResponse{
		OK:    false,
		Error: message,
	})
}

func WriteLoginCheckError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, LoginCheckResponse{
		OK:    false,
		Error: message,
	})
}
