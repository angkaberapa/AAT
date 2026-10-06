package token

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strings"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (handler *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /token", handler.issue)
	mux.HandleFunc("POST /token/refresh", handler.refresh)
	mux.HandleFunc("GET /.well-known/jwks.json", handler.jwks)
}

type credentialsRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (handler *Handler) issue(w http.ResponseWriter, request *http.Request) {
	var credentials credentialsRequest
	if err := decodeRequest(w, request, &credentials); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	pair, err := handler.service.Issue(credentials.ClientID, credentials.ClientSecret)
	if errors.Is(err, ErrInvalidCredentials) {
		w.Header().Set("WWW-Authenticate", `Basic realm="bnpb-auth"`)
		writeError(w, http.StatusUnauthorized, "invalid_client", "invalid client credentials")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "could not issue token")
		return
	}
	writeToken(w, pair)
}

func (handler *Handler) refresh(w http.ResponseWriter, request *http.Request) {
	var input refreshRequest
	if err := decodeRequest(w, request, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	pair, err := handler.service.Refresh(input.RefreshToken)
	if errors.Is(err, ErrInvalidRefreshToken) {
		writeError(w, http.StatusUnauthorized, "invalid_grant", "invalid or expired refresh token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "could not refresh token")
		return
	}
	writeToken(w, pair)
}

func (handler *Handler) jwks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, handler.service.JWKS())
}

func decodeRequest(w http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(w, request.Body, 64<<10)
	mediaType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if mediaType == "application/x-www-form-urlencoded" {
		if err := request.ParseForm(); err != nil {
			return err
		}
		switch value := target.(type) {
		case *credentialsRequest:
			value.ClientID = request.Form.Get("client_id")
			value.ClientSecret = request.Form.Get("client_secret")
		case *refreshRequest:
			value.RefreshToken = request.Form.Get("refresh_token")
		}
		return nil
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeToken(w http.ResponseWriter, pair Pair) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, pair)
}

func writeError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{
		"error":             code,
		"error_description": strings.TrimSpace(description),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
