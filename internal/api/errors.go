package api

import (
	"encoding/json"
	"io"
	"net/http"
)

// maxBodySize is the maximum allowed request body size (1 MB).
const maxBodySize = 1 << 20

// errorEnvelope is the standard error response shape.
type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes a JSON error response with the given status code.
func writeError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(errorEnvelope{
		Error: errorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// writeJSON writes a JSON response with the given status code and data.
func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

// readJSON decodes the request body into v, enforcing a size limit.
func readJSON(r *http.Request, v interface{}) error {
	lr := io.LimitReader(r.Body, maxBodySize)
	return json.NewDecoder(lr).Decode(v)
}
