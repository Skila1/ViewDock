package httpapi

import (
	"encoding/json"
	"net/http"
)

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteErr(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, ErrorBody{Code: code, Message: msg})
}

func WriteOK(w http.ResponseWriter) {
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
