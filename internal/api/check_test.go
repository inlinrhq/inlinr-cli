package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckToken(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusOK, nil},
		{http.StatusUnauthorized, ErrAuth},
		{http.StatusInternalServerError, ErrTransient},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/me/today" || r.Header.Get("Authorization") != "Bearer in_d_x" {
				t.Errorf("unexpected request %s %q", r.URL.Path, r.Header.Get("Authorization"))
			}
			w.WriteHeader(tc.status)
		}))
		err := New(srv.URL, "in_d_x", "test").CheckToken(context.Background())
		srv.Close()
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("HTTP %d: got %v, want %v", tc.status, err, tc.want)
		}
	}

	// Nobody listening: transient, never "refused".
	err := New("http://127.0.0.1:1", "in_d_x", "test").CheckToken(context.Background())
	if !errors.Is(err, ErrTransient) {
		t.Errorf("unreachable: got %v", err)
	}
}
