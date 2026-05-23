package handlers

import "net/http"

func (h *Handlers) OAuthStart(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	if account == "" {
		writeError(w, http.StatusBadRequest, "query param 'account' required (personal|work)")
		return
	}
	url, err := h.OAuth.StartURL(account)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (h *Handlers) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		writeError(w, http.StatusBadRequest, "google: "+e)
		return
	}
	account, email, err := h.OAuth.Complete(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"account": account, "email": email})
}
