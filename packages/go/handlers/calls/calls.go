// Package calls mounts the call routes from docs/contracts.md on the call service:
// create/read (#18), start (#11, mock only in this build), instructions (#28),
// approval (#29), control (#31) and summary (#39), plus the mock scenario presets the
// demo app offers. Every call route requires a session; another owner's call is 404.
package calls

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/auth"
	"github.com/andrewkilp/CSC4330_Project6/packages/go/calls"
)

func write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func fail(w http.ResponseWriter, e *calls.Error) {
	body := map[string]string{"code": e.Code, "message": e.Message}
	if e.Field != "" {
		body["field"] = e.Field
	}
	write(w, e.Status, map[string]any{"error": body})
}

// body enforces JSON, same-site requests and the 16 KiB mutation limit.
func body(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, &calls.Error{Status: 400, Code: "invalid_argument", Message: "The request origin is not allowed.", Field: "origin"})
		return nil, false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, &calls.Error{Status: 400, Code: "invalid_argument", Message: "Expected a JSON request.", Field: "request"})
		return nil, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil {
		fail(w, &calls.Error{Status: 400, Code: "invalid_argument", Message: "The request body is too large.", Field: "request"})
		return nil, false
	}
	return data, true
}

func Register(mux *http.ServeMux, svc *calls.Service, requireOwner func(http.Handler) http.Handler) {
	owned := func(pattern string, h func(w http.ResponseWriter, r *http.Request, owner string)) {
		mux.Handle(pattern, requireOwner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			owner, ok := auth.OwnerID(r.Context())
			if !ok {
				fail(w, &calls.Error{Status: 401, Code: "unauthenticated", Message: "A valid session is required."})
				return
			}
			h(w, r, owner)
		})))
	}
	mux.HandleFunc("GET /v1/mock/scenarios", func(w http.ResponseWriter, _ *http.Request) {
		list, err := svc.Scenarios()
		if err != nil {
			fail(w, &calls.Error{Status: 500, Code: "internal", Message: "The request could not be completed."})
			return
		}
		write(w, 200, map[string]any{"scenarios": list})
	})
	owned("POST /v1/calls", func(w http.ResponseWriter, r *http.Request, owner string) {
		data, ok := body(w, r)
		if !ok {
			return
		}
		req, e := calls.ParseCreate(data)
		if e != nil {
			fail(w, e)
			return
		}
		call, e := svc.Create(r.Context(), owner, req)
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 201, call)
	})
	owned("GET /v1/calls/{id}", func(w http.ResponseWriter, r *http.Request, owner string) {
		call, e := svc.Get(r.Context(), owner, r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, call)
	})
	owned("GET /v1/calls/{id}/summary", func(w http.ResponseWriter, r *http.Request, owner string) {
		summary, e := svc.Summary(r.Context(), owner, r.PathValue("id"))
		if e != nil {
			fail(w, e)
			return
		}
		write(w, 200, summary)
	})
	action := func(pattern string, parse func([]byte) (calls.Action, *calls.Error),
		run func(r *http.Request, owner string, a calls.Action) (any, *calls.Error)) {
		owned(pattern, func(w http.ResponseWriter, r *http.Request, owner string) {
			data, ok := body(w, r)
			if !ok {
				return
			}
			a, e := parse(data)
			if e != nil {
				fail(w, e)
				return
			}
			out, e := run(r, owner, a)
			if e != nil {
				fail(w, e)
				return
			}
			status := 200
			if pattern == "POST /v1/calls/{id}/instructions" {
				status = 201
			}
			write(w, status, out)
		})
	}
	action("POST /v1/calls/{id}/start", calls.ParseStart, func(r *http.Request, owner string, a calls.Action) (any, *calls.Error) {
		return svc.Start(r.Context(), owner, r.PathValue("id"), a)
	})
	action("POST /v1/calls/{id}/instructions", calls.ParseInstruction, func(r *http.Request, owner string, a calls.Action) (any, *calls.Error) {
		return svc.Instruct(r.Context(), owner, r.PathValue("id"), a)
	})
	action("POST /v1/calls/{id}/responses/{response_id}/approve", calls.ParseApproval, func(r *http.Request, owner string, a calls.Action) (any, *calls.Error) {
		return svc.Approve(r.Context(), owner, r.PathValue("id"), r.PathValue("response_id"), a)
	})
	action("POST /v1/calls/{id}/control", calls.ParseControl, func(r *http.Request, owner string, a calls.Action) (any, *calls.Error) {
		return svc.Control(r.Context(), owner, r.PathValue("id"), a)
	})
}
