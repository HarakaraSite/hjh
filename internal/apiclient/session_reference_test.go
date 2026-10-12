package apiclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/sessionid"
)

func TestHumanSessionReferenceUsesCompleteAPIIdentity(t *testing.T) {
	ids := []string{"b71a29a6-7552-485d-9922-a1d7ae426706", "b71a29a6-a552-485d-9922-a1d7ae426706"}
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/sessions" {
			json.NewEncoder(w).Encode([]api.Session{{ID: ids[0]}, {ID: ids[1]}})
			return
		}
		path = r.URL.Path
		if path != "/api/v2/sessions/"+ids[0] {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(api.Session{ID: ids[0]})
	}))
	defer server.Close()
	client, err := apiclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	short := sessionid.Short(ids[0], ids...)
	if short != "b71a29a6-7" {
		t.Fatalf("indistinguishable human ID: %s", short)
	}
	session, err := client.ResolveSession(context.Background(), strings.ToUpper(short))
	if err != nil || session.ID != ids[0] || path != "/api/v2/sessions/"+ids[0] {
		t.Fatalf("human selector/API identity mismatch: %+v %s %v", session, path, err)
	}
	path = ""
	_, err = client.ResolveSession(context.Background(), "b71a29a6")
	if err == nil || !strings.Contains(err.Error(), short) || path != "" {
		t.Fatalf("ambiguous selector chose a Session: %s %v", path, err)
	}
}
