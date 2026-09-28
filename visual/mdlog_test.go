package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func linkTemp(t *testing.T) (root, file string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("LEARN_ROOT", root)
	file = filepath.Join(root, "lesson.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := logLink(root, "lesson.md"); err != nil {
		t.Fatal(err)
	}
	return root, file
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		f.WriteString(l + "\n")
	}
}

func hook(t *testing.T, agent, payload string) {
	t.Helper()
	if err := runHook(agent, strings.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeHook(t *testing.T) {
	root, file := linkTemp(t)
	tr := filepath.Join(root, "t.jsonl")
	stop := `{"hook_event_name":"Stop","transcript_path":"` + tr + `"}`
	appendLines(t, tr,
		`{"type":"user","message":{"content":"what is $x^2$?"}}`,
		`{"type":"user","isMeta":true,"message":{"content":"skill text"}}`,
		`{"type":"user","message":{"content":"<command-name>/md-log</command-name><command-args>lesson.md</command-args>"}}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"A square."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"bash output"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Pick a goal."},{"type":"tool_use","id":"a1","name":"AskUserQuestion","input":{"questions":[{"question":"Goal?","header":"Goal","options":[{"label":"Proofs"},{"label":"Intuition"}]}]}}]}}`,
	)
	hook(t, "claude", `{"hook_event_name":"PreToolUse","tool_name":"AskUserQuestion","tool_use_id":"a1","transcript_path":"`+tr+`","tool_input":{"questions":[{"question":"Goal?","header":"Goal","options":[{"label":"Proofs"},{"label":"Intuition"}]}]}}`)
	appendLines(t, tr,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a1","content":"User answered: Goal=Intuition"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Quiz time."},{"type":"tool_use","id":"q1","name":"mcp__learn-visual__quiz","input":{}}]}}`,
	)
	hook(t, "claude", `{"hook_event_name":"PreToolUse","tool_name":"mcp__learn-visual__quiz","transcript_path":"`+tr+`"}`)
	logQuiz("> [!question] Quiz\n> Q?")
	appendLines(t, tr,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"q1","content":"graded"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Well done."}]}}`,
	)
	hook(t, "claude", stop)
	hook(t, "claude", stop)

	got, _ := os.ReadFile(file)
	want := []string{"> [!quote] YOU\n\nwhat is $x^2$?", "> [!abstract] CLAUDE\n\nA square.\n\nPick a goal.",
		"> [!question] Question — Goal\n> Goal?\n>\n> 1. Proofs\n> 2. Intuition", "> [!example] Answer\n> User answered: Goal=Intuition",
		"> [!abstract] CLAUDE\n\nQuiz time.", "> [!question] Quiz\n> Q?", "> [!abstract] CLAUDE\n\nWell done.\n"}
	if s := string(got); s != strings.Join(want, "\n\n") {
		t.Fatalf("log:\n%s", s)
	}
}

func TestTurnHooks(t *testing.T) {
	_, file := linkTemp(t)
	hook(t, "gemini", `{"hook_event_name":"BeforeAgent","prompt":"hi gemini"}`)
	hook(t, "gemini", `{"hook_event_name":"BeforeAgent","prompt":"/md-log x.md"}`)
	hook(t, "gemini", `{"hook_event_name":"AfterAgent","prompt":"p","prompt_response":"gem"}`)
	hook(t, "opencode", `{"session_id":"s1","messages":[{"info":{"role":"user"},"parts":[{"type":"text","text":"hi oc"}]},{"info":{"role":"assistant"},"parts":[{"type":"reasoning","text":"x"},{"type":"text","text":"yo"}]}]}`)
	hook(t, "opencode", `{"session_id":"s1","messages":[{"info":{"role":"user"},"parts":[{"type":"text","text":"hi oc"}]},{"info":{"role":"assistant"},"parts":[{"type":"text","text":"yo"}]},{"info":{"role":"user"},"parts":[{"type":"text","text":"again","synthetic":false},{"type":"text","text":"sys","synthetic":true}]}]}`)

	got, _ := os.ReadFile(file)
	want := strings.Join([]string{"> [!quote] YOU\n\nhi gemini", "> [!abstract] GEMINI\n\ngem",
		"> [!quote] YOU\n\nhi oc", "> [!abstract] OPENCODE\n\nyo", "> [!quote] YOU\n\nagain\n"}, "\n\n")
	if string(got) != want {
		t.Fatalf("log:\n%s", got)
	}
}
