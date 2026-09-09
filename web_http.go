package main

import (
	"io/fs"
	"net/http"
	"strings"
)

// LoginPageHandler serves the sign-in page, redirecting to the app if the
// visitor already holds a valid session.
func (a *Auth) LoginPageHandler(view *views) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.validSession(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		renderView(w, view.loginT, "login", nil)
	}
}

func (a *Auth) validSession(r *http.Request) bool {
	token := sessionToken(r)
	if token == "" {
		return false
	}
	_, err := a.Sessions.GetByToken(r.Context(), token)
	return err == nil
}

// csrfTokenFromRequest returns the double-submit CSRF cookie value so the
// layout can echo it back to every HTMX request via the X-CSRF-Token header.
func csrfTokenFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(webFS, "web/static")
	if err != nil {
		panic(err)
	}

	files := http.FileServer(http.FS(sub))
	inner := http.StripPrefix("/static/", files)

	// Serve individual files but never directory listings, which would leak the
	// names of our assets and any ordering/size metadata.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
}
