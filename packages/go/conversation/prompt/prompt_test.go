package prompt

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/andrewkilp/CSC4330_Project6/packages/go/claude"
)

func TestGoldenScenarios(t *testing.T) {
	data, err := os.ReadFile("testdata/scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name         string
		Goal         json.RawMessage
		Turn         string
		Instructions []string
		Reply        string
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			in := claude.Input{Goal: tc.Goal, Transcript: []claude.Turn{{Speaker: "business", Text: tc.Turn}}, Instructions: tc.Instructions, ContextRevision: 2}
			got, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Goal) != string(tc.Goal) || !reflect.DeepEqual(got.Transcript, in.Transcript) || !reflect.DeepEqual(got.Instructions, tc.Instructions) || got.ContextRevision != 2 || got.SystemPolicy != Policy {
				t.Fatalf("context changed: %+v", got)
			}
			payload, _ := json.Marshal(got)
			if strings.Contains(string(payload), "SystemPolicy") || strings.Contains(Policy, tc.Turn) {
				t.Fatal("remote data entered system policy")
			}
			d, err := Validate(got, claude.Update{Text: tc.Reply, ContextRevision: 2, Final: true})
			if err != nil || !d.NeedsApproval || d.Reason == "" {
				t.Fatalf("approval: %+v %v", d, err)
			}
			if _, err := Validate(got, claude.Update{Text: "Use +12025550199 or invented@example.com", ContextRevision: 2, Final: true}); err == nil {
				t.Fatal("invented contact accepted")
			}
			got.Goal[0] = 'x'
			got.Transcript[0].Text = "changed"
			got.Instructions[0] = "changed"
			if in.Goal[0] == 'x' || in.Transcript[0].Text == "changed" || in.Instructions[0] == "changed" {
				t.Fatal("caller context mutated")
			}
		})
	}
}

func TestValidationFailures(t *testing.T) {
	in := claude.Input{Goal: json.RawMessage(`{"kind":"restaurant"}`), Transcript: []claude.Turn{{Speaker: "business", Text: "Hello"}}, ContextRevision: 4}
	for _, update := range []claude.Update{
		{Text: "Valid text", ContextRevision: 3, Final: true},
		{Text: "Valid text", ContextRevision: 4},
		{Text: " ", ContextRevision: 4, Final: true},
		{Text: strings.Repeat("😀", 751), ContextRevision: 4, Final: true},
		{Text: string([]byte{0xff}), ContextRevision: 4, Final: true},
	} {
		if _, err := Validate(in, update); err == nil {
			t.Fatalf("accepted %+v", update)
		}
	}
	if _, err := Validate(in, claude.Update{Text: strings.Repeat("😀", 750), ContextRevision: 4, Final: true}); err != nil {
		t.Fatal(err)
	}
	for _, goal := range []string{`null`, `[]`, `{"kind":"payment"}`, `broken`} {
		bad := in
		bad.Goal = json.RawMessage(goal)
		if _, err := Build(bad); err == nil {
			t.Fatal("accepted", goal)
		}
	}
	in.Instructions = []string{strings.Repeat("😀", 501)}
	if _, err := Build(in); err == nil {
		t.Fatal("UTF-16 instruction limit")
	}
}

func TestAuthorizedContact(t *testing.T) {
	in := claude.Input{Goal: json.RawMessage(`{"contact_phone":"+12025550100"}`)}
	if _, err := Validate(in, claude.Update{Text: "Use +12025550100", Final: true}); err != nil {
		t.Fatal(err)
	}
}
