package calls_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	svc "github.com/andrewkilp/CSC4330_Project6/packages/go/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/events"
	handler "github.com/andrewkilp/CSC4330_Project6/packages/go/handlers/calls"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/repository/repositorytest"
)

func TestRoutes(t *testing.T) {
	service, err := svc.New(svc.Config{Repo: repositorytest.Open(t).Repo, Hub: events.NewHub(0)})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions()
	mux := http.NewServeMux()
	handler.Register(mux, service, sessions.RequireOwner)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	token, _ := sessions.Create()
	other, _ := sessions.Create()

	do := func(method, path, cookie string, body any, header map[string]string) (int, map[string]any) {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req, _ := http.NewRequest(method, srv.URL+path, &buf)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if cookie != "" {
			req.Header.Set("Cookie", auth.CookieName+"="+cookie)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, list := do("GET", "/v1/mock/scenarios", "", nil, nil)
	scenarios, _ := list["scenarios"].([]any)
	if code != 200 || len(scenarios) != 2 {
		t.Fatalf("scenarios = %d %v", code, list)
	}
	s := scenarios[0].(map[string]any)
	create := map[string]any{"mode": "mock", "destination": s["destination"], "business_name": s["business_name"], "goal": s["goal"],
		"approval_mode": s["approval_mode"], "consent_acknowledged": true, "idempotency_key": repository.NewID()}
	if code, _ := do("POST", "/v1/calls", "", create, nil); code != 401 {
		t.Fatalf("no session = %d", code)
	}
	code, call := do("POST", "/v1/calls", token, create, nil)
	if code != 201 || call["status"] != "draft" || call["owner_id"] == "" {
		t.Fatalf("create = %d %v", code, call)
	}
	id := call["id"].(string)
	if code, again := do("POST", "/v1/calls", token, create, nil); code != 201 || again["id"] != id {
		t.Fatalf("idempotent create = %d %v", code, again)
	}
	create["business_name"] = "Changed"
	if code, e := do("POST", "/v1/calls", token, create, nil); code != 409 || !strings.Contains(e["error"].(map[string]any)["field"].(string), "idempotency_key") {
		t.Fatalf("reused key = %d %v", code, e)
	}
	if code, _ := do("GET", "/v1/calls/"+id, other, nil, nil); code != 404 {
		t.Fatalf("wrong owner read = %d", code)
	}
	if code, _ := do("POST", "/v1/calls/"+id+"/start", other, map[string]any{"idempotency_key": repository.NewID()}, nil); code != 404 {
		t.Fatalf("wrong owner start = %d", code)
	}
	if code, e := do("POST", "/v1/calls/"+id+"/start", token, map[string]any{"idempotency_key": repository.NewID()},
		map[string]string{"Sec-Fetch-Site": "cross-site"}); code != 400 || e["error"].(map[string]any)["field"] != "origin" {
		t.Fatalf("cross-site start = %d %v", code, e)
	}
	if code, e := do("POST", "/v1/calls/"+id+"/control", token, map[string]any{"action": "explode", "idempotency_key": repository.NewID()}, nil); code != 400 ||
		e["error"].(map[string]any)["field"] != "action" {
		t.Fatalf("bad control = %d %v", code, e)
	}
	if code, e := do("GET", "/v1/calls/"+id+"/summary", token, nil, nil); code != 409 || e["error"].(map[string]any)["field"] != "status" {
		t.Fatalf("summary before end = %d %v", code, e)
	}
	if code, started := do("POST", "/v1/calls/"+id+"/start", token, map[string]any{"idempotency_key": repository.NewID()}, nil); code != 200 || started["status"] != "dialing" {
		t.Fatalf("start = %d %v", code, started)
	}
	if code, ended := do("POST", "/v1/calls/"+id+"/control", token, map[string]any{"action": "end", "idempotency_key": repository.NewID()}, nil); code != 200 || ended["status"] != "ended" {
		t.Fatalf("end = %d %v", code, ended)
	}
}
