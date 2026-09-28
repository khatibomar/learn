package main

import (
	"context"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func callQuiz(t *testing.T, args map[string]any, answer func(*mcp.ElicitParams) *mcp.ElicitResult) string {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "test"}, nil)
	addQuizTool(server)
	var opts *mcp.ClientOptions
	if answer != nil {
		opts = &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return answer(req.Params), nil
		}}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "client"}, opts)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "quiz", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func schemaOf(t *testing.T, p *mcp.ElicitParams) map[string]any {
	t.Helper()
	b, err := json.Marshal(p.RequestedSchema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

var planets = map[string]any{
	"question":      "Closest planet to the Sun?",
	"options":       []map[string]any{{"label": "Venus"}, {"label": "Mercury", "value": "mercury"}, {"label": "Mars"}},
	"correctAnswer": "mercury",
	"explanation":   "Mercury orbits at 0.39 AU.",
	"shuffle":       false,
}

func TestQuizSingleSelect(t *testing.T) {
	var sent map[string]any
	out := callQuiz(t, planets, func(p *mcp.ElicitParams) *mcp.ElicitResult {
		sent = schemaOf(t, p)
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"answer": "2. Mercury", "s_note": "easy"}}
	})
	if sent == nil {
		t.Fatalf("no form sent: %s", out)
	}
	enum := sent["properties"].(map[string]any)["answer"].(map[string]any)["enum"].([]any)
	if len(enum) != 4 || enum[3] != "0. I don't know" {
		t.Fatalf("enum = %v", enum)
	}
	for _, want := range []string{"answered correctly", "Correct: 2. Mercury", "User's note: easy", "0.39 AU"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestQuizDontKnow(t *testing.T) {
	out := callQuiz(t, planets, func(*mcp.ElicitParams) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"answer": "0. I don't know"}}
	})
	if !strings.Contains(out, "did not guess") || strings.Contains(out, "incorrectly") {
		t.Fatal(out)
	}
}

func TestQuizMultiSelect(t *testing.T) {
	args := map[string]any{
		"question":      "Which are prime?",
		"options":       []map[string]any{{"label": "2"}, {"label": "4"}, {"label": "7"}},
		"multiSelect":   true,
		"correctAnswer": `["2", "7"]`,
		"explanation":   "4 = 2·2.",
		"shuffle":       false,
	}
	out := callQuiz(t, args, func(*mcp.ElicitParams) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"q01": true, "q02": true, "q03": true}}
	})
	if !strings.Contains(out, "answered incorrectly") || !strings.Contains(out, "Correct: 1. 2, 3. 7") {
		t.Fatal(out)
	}
}

func TestQuizNoElicitation(t *testing.T) {
	out := callQuiz(t, planets, nil)
	if !strings.Contains(out, "not available") {
		t.Fatal(out)
	}
}

func TestQuizBadCorrectAnswer(t *testing.T) {
	args := map[string]any{}
	for k, v := range planets {
		args[k] = v
	}
	args["correctAnswer"] = "pluto"
	if out := callQuiz(t, args, nil); !strings.Contains(out, `"pluto" matches no option`) {
		t.Fatal(out)
	}
}
