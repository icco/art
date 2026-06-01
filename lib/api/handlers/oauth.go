package handlers

import (
	"html"
	"net/http"
)

// OAuthStart returns a Google consent URL for the requested account.
func (h *Handlers) OAuthStart(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	if account == "" {
		writeError(w, r, http.StatusBadRequest, "query param 'account' required (personal|work)")
		return
	}
	url, err := h.OAuth.StartURL(account)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"url": url})
}

// OAuthCallback completes the OAuth flow and renders a small HTML page.
func (h *Handlers) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		writeCallbackHTML(w, http.StatusBadRequest, "google declined", html.EscapeString(e))
		return
	}
	account, email, err := h.OAuth.Complete(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		writeCallbackHTML(w, http.StatusBadRequest, "link failed", html.EscapeString(err.Error()))
		return
	}
	writeCallbackHTML(w, http.StatusOK, "linked",
		html.EscapeString(account)+" account linked as "+html.EscapeString(email)+
			". You can close this tab.")
}

func writeCallbackHTML(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><title>art: " +
		title + "</title><body style=\"font-family:system-ui;padding:2rem\"><h1>art</h1><p>" +
		body + "</p>"))
}
