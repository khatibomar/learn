package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const dontKnowLabel = "I don't know"

// QuizOption is one answer option of a quiz question.
type QuizOption struct {
	Label       string `json:"label" jsonschema:"Display label for the answer option."`
	Value       string `json:"value,omitempty" jsonschema:"Stable value that correctAnswer refers to. Defaults to the label."`
	Description string `json:"description,omitempty" jsonschema:"Optional extra detail shown after the label."`
}

// QuizInput is the input of the quiz tool.
type QuizInput struct {
	Question      string       `json:"question" jsonschema:"The single quiz question. Ask exactly one question per call."`
	Details       string       `json:"details,omitempty" jsonschema:"Optional extra context shown under the question."`
	Options       []QuizOption `json:"options" jsonschema:"The real, gradable answer options (2 or more). Do not add an 'I don't know' option: the tool adds it."`
	MultiSelect   bool         `json:"multiSelect,omitempty" jsonschema:"True when more than one option is correct and the user must select all of them."`
	CorrectAnswer answerList   `json:"correctAnswer" jsonschema:"The option value(s) of the correct answer, not position numbers. Single-select: one item. Multi-select: every correct value."`
	Explanation   string       `json:"explanation" jsonschema:"Why the correct answer is correct. The user sees it only after the answer."`
	Shuffle       *bool        `json:"shuffle,omitempty" jsonschema:"Defaults to true. Set to false only when option order has a meaning."`
}

// answerList also accepts a single string or a JSON-encoded array in a string.
type answerList []string

func (a *answerList) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*a = list
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return errors.New("correctAnswer must be a string or an array of strings")
	}
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "[") && json.Unmarshal([]byte(t), &list) == nil {
		*a = list
		return nil
	}
	*a = []string{s}
	return nil
}

const quizDescription = "Ask the user a GRADED multiple-choice question with a known correct answer. " +
	"The user answers in a form. The tool shuffles the options, grades the answer, and returns the verdict, " +
	"the correct answer, the user's optional note, and your explanation. The user does not see the correct " +
	"answer or the explanation until you show them. Use it to find what the learner knows, and to check " +
	"each idea after you teach it. For questions with no right answer, use your normal question tool.\n\n" +
	"Rules:\n" +
	"- correctAnswer holds option VALUES, not positions. A value that matches no option is an error.\n" +
	"- explanation is required: say why the correct answer is correct.\n" +
	"- Multi-select is graded as an exact set: the user must select every correct option and no other.\n" +
	"- The tool always adds an 'I don't know' choice. Do not add your own. An 'I don't know' answer is a real " +
	"gap to teach, not a wrong guess.\n" +
	"- Make each wrong option a specific, believable mistake, so the chosen option shows the misconception. " +
	"Every wrong option must be clearly wrong. No trick questions.\n" +
	"- Keep options similar in length, detail, and form, so the correct one does not stand out.\n" +
	"- After the result, show the user the verdict (✓ Correct / ✗ Incorrect / I don't know), the correct " +
	"answer, and the explanation. Read the note if there is one.\n" +
	"- If the result says the form is not available, ask the same question in chat instead."

func addQuizTool(server *mcp.Server) {
	schema, err := jsonschema.For[QuizInput](nil)
	if err != nil {
		panic(err)
	}
	answer := schema.Properties["correctAnswer"]
	schema.Properties["correctAnswer"] = &jsonschema.Schema{
		Description: answer.Description,
		AnyOf:       []*jsonschema.Schema{{Type: "string"}, {Type: "array", Items: &jsonschema.Schema{Type: "string"}}},
	}
	tool := &mcp.Tool{Name: "quiz", Title: "Quiz", Description: quizDescription, InputSchema: schema}
	mcp.AddTool(server, tool, runQuiz)
}

type quizOption struct {
	QuizOption
	correct bool
}

// quizStore keeps the shuffled options between the form request and the answer.
var quizStore = struct {
	sync.Mutex
	m map[string][]quizOption
}{m: map[string][]quizOption{}}

// runQuiz sends the form as a multi round-trip input request (SEP-2322). The
// SDK sends it as elicitation/create to clients on older protocol versions.
func runQuiz(ctx context.Context, req *mcp.CallToolRequest, in QuizInput) (*mcp.CallToolResult, any, error) {
	if state := req.Params.RequestState; state != "" {
		quizStore.Lock()
		opts, ok := quizStore.m[state]
		delete(quizStore.m, state)
		quizStore.Unlock()
		if !ok {
			return toolText(true, "quiz: unknown request state; call quiz again"), nil, nil
		}
		res, _ := req.Params.InputResponses["quiz"].(*mcp.ElicitResult)
		if res == nil {
			return toolText(true, "quiz: no form answer in the retry"), nil, nil
		}
		if res.Action != "accept" {
			logQuiz(callout("warning", "Quiz — cancelled", []string{"(user skipped)"}))
			return toolText(false, "User dismissed the quiz without an answer ("+res.Action+")."), nil, nil
		}
		text, block := gradeQuiz(in, opts, res.Content)
		logQuiz(block)
		return toolText(false, text), nil, nil
	}

	opts, err := prepareQuiz(in)
	if err != nil {
		return toolText(true, "quiz: "+err.Error()), nil, nil
	}
	if p := req.Session.InitializeParams(); p == nil || p.Capabilities == nil || p.Capabilities.Elicitation == nil {
		return toolText(false, "The quiz form is not available: this client does not support MCP elicitation. "+
			"Ask the same question in chat: number the options, add '0. I don't know', and grade the reply."), nil, nil
	}
	shown := make([]string, len(opts))
	for i, o := range opts {
		shown[i] = optionText(i, o)
	}
	logQuiz(questionCallout("Quiz", in.Question, in.Details, shown))
	state := rand.Text()
	quizStore.Lock()
	quizStore.m[state] = opts
	quizStore.Unlock()
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{"quiz": quizForm(in, opts)},
		RequestState:  state,
	}, nil, nil
}

// prepareQuiz checks the input and returns the options in display order.
func prepareQuiz(in QuizInput) ([]quizOption, error) {
	var opts []quizOption
	seen := map[string]bool{}
	for _, o := range in.Options {
		o.Label = strings.TrimSpace(o.Label)
		if o.Label == "" {
			continue
		}
		o.Value = cmp.Or(strings.TrimSpace(o.Value), o.Label)
		o.Description = strings.TrimSpace(o.Description)
		if seen[o.Value] {
			return nil, fmt.Errorf("duplicate option value %q", o.Value)
		}
		seen[o.Value] = true
		opts = append(opts, quizOption{QuizOption: o})
	}
	if len(opts) < 2 {
		return nil, errors.New("give at least two options")
	}
	if len(in.CorrectAnswer) == 0 {
		return nil, errors.New("correctAnswer is required")
	}
	for _, v := range in.CorrectAnswer {
		i := slices.IndexFunc(opts, func(o quizOption) bool { return o.Value == strings.TrimSpace(v) })
		if i < 0 {
			values := make([]string, len(opts))
			for j, o := range opts {
				values[j] = fmt.Sprintf("%q", o.Value)
			}
			return nil, fmt.Errorf("correctAnswer %q matches no option value (%s)", v, strings.Join(values, ", "))
		}
		opts[i].correct = true
	}
	if !in.MultiSelect && len(in.CorrectAnswer) > 1 {
		return nil, errors.New("single-select question has more than one correct value; set multiSelect")
	}
	if strings.TrimSpace(in.Explanation) == "" {
		return nil, errors.New("explanation is required")
	}
	if in.Shuffle == nil || *in.Shuffle {
		mrand.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	}
	return opts, nil
}

func optionText(i int, o quizOption) string {
	s := fmt.Sprintf("%d. %s", i+1, o.Label)
	if o.Description != "" {
		s += " — " + o.Description
	}
	return s
}

// quizForm builds the elicitation form. Property keys sort in display order,
// and the schema is raw JSON so that clients get the properties in that order.
func quizForm(in QuizInput, opts []quizOption) *mcp.ElicitParams {
	msg := strings.TrimSpace(in.Question)
	if d := strings.TrimSpace(in.Details); d != "" {
		msg += "\n\n" + d
	}
	var props [][2]any
	if in.MultiSelect {
		msg += "\n\nSelect all that apply."
		for i, o := range opts {
			props = append(props, [2]any{fmt.Sprintf("q%02d", i+1),
				map[string]any{"type": "boolean", "title": optionText(i, o), "default": false}})
		}
		props = append(props, [2]any{"r_dont_know",
			map[string]any{"type": "boolean", "title": "0. " + dontKnowLabel, "default": false}})
	} else {
		choices := make([]string, 0, len(opts)+1)
		for i, o := range opts {
			choices = append(choices, optionText(i, o))
		}
		choices = append(choices, "0. "+dontKnowLabel)
		props = append(props, [2]any{"answer",
			map[string]any{"type": "string", "title": "Answer", "enum": choices}})
	}
	props = append(props, [2]any{"s_note", map[string]any{"type": "string", "title": "Note (optional)",
		"description": "Anything you want to add: what you thought, or what is unclear."}})

	var b bytes.Buffer
	b.WriteString(`{"type":"object","properties":{`)
	for i, p := range props {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(p[0])
		v, _ := json.Marshal(p[1], json.Deterministic(true))
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteString(`}`)
	if !in.MultiSelect {
		b.WriteString(`,"required":["answer"]`)
	}
	b.WriteString(`}`)
	return &mcp.ElicitParams{Message: msg, RequestedSchema: jsonv1.RawMessage(b.Bytes())}
}

// logQuiz appends a block to the md-log file, if one is linked.
func logQuiz(block string) {
	root, err := learnRoot()
	if err == nil {
		err = withLog(root, func(_ *logState, w *logWriter) error { w.add(block); return nil })
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "learn-visual: quiz log:", err)
	}
}

// gradeQuiz reads the form answer. It returns the result for the agent and
// the answer block for the md-log file.
func gradeQuiz(in QuizInput, opts []quizOption, content map[string]any) (string, string) {
	var selected []int
	dontKnow := false
	if in.MultiSelect {
		for i := range opts {
			if v, _ := content[fmt.Sprintf("q%02d", i+1)].(bool); v {
				selected = append(selected, i)
			}
		}
		dk, _ := content["r_dont_know"].(bool)
		dontKnow = dk || len(selected) == 0
	} else {
		answer, _ := content["answer"].(string)
		for i, o := range opts {
			if answer == optionText(i, o) {
				selected = append(selected, i)
			}
		}
		dontKnow = len(selected) == 0
	}
	note, _ := content["s_note"].(string)
	note = strings.TrimSpace(note)

	var correct, correctNums []string
	allRight := !dontKnow
	for i, o := range opts {
		if o.correct {
			correct = append(correct, fmt.Sprintf("%d. %s", i+1, o.Label))
			correctNums = append(correctNums, fmt.Sprint(i+1))
		}
		if o.correct != slices.Contains(selected, i) {
			allRight = false
		}
	}

	picked := make([]string, len(selected))
	for j, i := range selected {
		picked[j] = fmt.Sprintf("%d. %s", i+1, opts[i].Label)
	}
	explanation := strings.TrimSpace(in.Explanation)

	var b strings.Builder
	if dontKnow {
		b.WriteString(`User selected "I don't know". They did not guess: this is a real knowledge gap, not a wrong answer.`)
	} else {
		verdict := "incorrectly"
		if allRight {
			verdict = "correctly"
		}
		fmt.Fprintf(&b, "User answered %s.\nSelected: %s", verdict, strings.Join(picked, ", "))
	}
	fmt.Fprintf(&b, "\nCorrect: %s", strings.Join(correct, ", "))
	if note != "" {
		fmt.Fprintf(&b, "\nUser's note: %s", note)
	}
	fmt.Fprintf(&b, "\nExplanation: %s", explanation)
	b.WriteString("\nOptions as shown: ")
	for i, o := range opts {
		if i > 0 {
			b.WriteString(" | ")
		}
		fmt.Fprintf(&b, "%d. %s", i+1, o.Label)
	}
	b.WriteString("\nShow the user the verdict, the correct answer, and the explanation now.")

	kind, title, answer := "failure", "Quiz — incorrect ✗", strings.Join(picked, ", ")
	switch {
	case dontKnow:
		kind, title, answer = "question", "Quiz — I don't know", dontKnowLabel
	case allRight:
		kind, title = "success", "Quiz — correct ✓"
	}
	body := []string{"Your answer: " + answer, "Correct answer: " + strings.Join(correctNums, ", ")}
	if note != "" {
		body = append(append(body, ""), strings.Split("Note: "+note, "\n")...)
	}
	body = append(append(body, ""), strings.Split(explanation, "\n")...)
	return b.String(), callout(kind, title, body)
}

func toolText(isError bool, text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: isError, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
