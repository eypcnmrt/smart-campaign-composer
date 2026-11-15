// Package compose turns a brief into a segment rule and into campaign text.
// It sits above the provider (package llm) and the domain checks (packages
// segment and content) and owns the repair loop: when the model's answer fails
// validation, the problems, with their paths, go back to the model, at most
// MaxRepairs times.
//
// Only a wrong answer is repaired. A refusal, a cut-off, a timeout, a rate
// limit or a provider error is returned as it is: asking again with a list of
// "problems" would not help, and the SDK has already retried what can be retried.
package compose

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
	"smartcampaign/internal/streamjson"
)

// MaxRepairs is how many times a failed answer is sent back for correction.
// A task therefore makes at most MaxRepairs+1 requests.
const MaxRepairs = 2

// Stages name the task in errors.
const (
	StageSegment = "segment"
	StageContent = "content"
)

// ErrRepairExhausted means the model's answer was still invalid after the last
// repair. The error is a *GenerationError.
var ErrRepairExhausted = errors.New("the model's answer is still invalid after repair")

// GenerationError is the controlled failure when repair did not help. It keeps
// the problems of the last attempt (paths and messages, safe to show) and the
// raw last answer for logs; Error() does not print the answer.
type GenerationError struct {
	Stage      string
	Attempts   int
	Problems   []string
	LastOutput string
}

func (e *GenerationError) Error() string {
	return fmt.Sprintf("%s: %s after %d attempts: %s", e.Stage, ErrRepairExhausted, e.Attempts, strings.Join(e.Problems, "; "))
}

func (e *GenerationError) Is(target error) bool { return target == ErrRepairExhausted }

// Composer generates segments and content with a Client.
type Composer struct {
	client   llm.Client
	rejected func(Rejection)
}

// New returns a Composer.
func New(client llm.Client) *Composer { return &Composer{client: client} }

// Problem is one reason an answer was rejected. Path is the field (a JSON path); Code is the validator's stable code and is
// empty where the validator has none (the segment rule's errors have a path and a message only); Message is the English text
// that also goes into the repair prompt.
type Problem struct {
	Path    string `json:"path,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

// String is the line the repair prompt carries.
func (p Problem) String() string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// Rejection is an answer that failed validation and was (or, after the last attempt, would have been) sent back.
type Rejection struct {
	Stage    string // StageSegment or StageContent
	Attempt  int    // 1-based: the attempt whose answer was rejected
	Problems []Problem
}

// OnRejected sets fn, called for every rejected answer, the last one of a failed task included. It is for evaluation and
// logs; fn must not block and runs on the goroutine that generates. Set it before the first use of the Composer.
func (c *Composer) OnRejected(fn func(Rejection)) { c.rejected = fn }

func (c *Composer) reject(stage string, attempt int, problems []Problem) {
	if c.rejected != nil {
		c.rejected(Rejection{Stage: stage, Attempt: attempt, Problems: problems})
	}
}

// SegmentResult is a validated segment rule.
type SegmentResult struct {
	Rule     segment.Rule
	JSON     string // the validated model output, kept for the approval snapshot
	Attempts int    // 1 when the first answer was valid
	Model    string
}

// Segment asks the model for the rule that matches the brief. The returned rule
// has passed segment.Decode and segment.Validate; UnsupportedCriteria is
// passed through for the UI to show and is not an error.
func (c *Composer) Segment(ctx context.Context, brief string) (SegmentResult, error) {
	task := segmentUser(brief)
	req := llm.Request{
		Task:   llm.TaskSegment,
		System: segmentSystem(),
		User:   task,
		Schema: segment.Schema(),
		Effort: llm.EffortLow,
		Brief:  brief,
	}
	var last llm.Response
	rule, attempts, err := repair(ctx, StageSegment, c.reject,
		func(ctx context.Context, prev *failure) (string, error) {
			if prev != nil {
				req.User = repairUser(task, prev.output, prev.problems)
			}
			resp, err := c.client.Complete(ctx, req)
			last = resp
			return resp.Text, err
		},
		func(text string) (segment.Rule, []Problem) {
			rule, err := segment.Decode([]byte(text))
			if err == nil {
				err = segment.Validate(rule)
			}
			if err != nil {
				return segment.Rule{}, problemsOf(err)
			}
			return rule, nil
		})
	if err != nil {
		return SegmentResult{}, err
	}
	return SegmentResult{Rule: rule, JSON: last.Text, Attempts: attempts, Model: last.ServedBy}, nil
}

// ContentInput is what the content is written from.
type ContentInput struct {
	Brief    string
	Audience string // one-line description of the segment, usually Rule.Explanation
}

// EventKind says what an Event carries.
type EventKind int

const (
	// EventAttempt starts an attempt. A UI discards what it showed so far: the
	// first attempt has just begun, or the previous one was rejected and the
	// model is writing the content again.
	EventAttempt EventKind = iota
	// EventDelta carries decoded text for one field (Field, Text).
	EventDelta
	// EventRaw carries raw JSON text and is sent instead of deltas once the
	// stream stopped looking like a flat object of strings (docs/PLAN.md
	// section 4.2: show the raw stream, the validated result replaces it).
	EventRaw
)

// Event is progress of Content.
type Event struct {
	Kind    EventKind
	Attempt int    // EventAttempt: 1-based
	Field   string // EventDelta
	Text    string // EventDelta, EventRaw
}

// ContentResult is validated campaign content.
type ContentResult struct {
	Content      content.Content
	JSON         string
	Attempts     int
	Model        string // model that wrote the final part of the answer
	FallbackUsed bool
}

// Content streams the model's answer for the brief. onEvent (may be nil)
// receives the progress as it happens, which is what the SSE endpoint forwards.
// The returned content has passed content.Parse and content.Validate; it, not
// the streamed text, is the result: streamed text is only a preview.
//
// If an error is returned, text shown from the last attempt is not an answer.
func (c *Composer) Content(ctx context.Context, in ContentInput, onEvent func(Event)) (ContentResult, error) {
	emit := func(e Event) {
		if onEvent != nil {
			onEvent(e)
		}
	}
	task := contentUser(in)
	req := llm.Request{
		Task:   llm.TaskContent,
		System: contentSystem(),
		User:   task,
		Schema: content.Schema(),
		Effort: llm.EffortMedium,
		Brief:  in.Brief,
	}
	var last llm.Response
	attempt := 0
	result, attempts, err := repair(ctx, StageContent, c.reject,
		func(ctx context.Context, prev *failure) (string, error) {
			attempt++
			if prev != nil {
				req.User = repairUser(task, prev.output, prev.problems)
			}
			emit(Event{Kind: EventAttempt, Attempt: attempt})

			extractor := streamjson.New(content.Fields...)
			var raw strings.Builder
			resp, err := c.client.Stream(ctx, req, func(piece string) {
				raw.WriteString(piece)
				if extractor.Err() != nil {
					emit(Event{Kind: EventRaw, Text: piece})
					return
				}
				for _, d := range extractor.Write(piece) {
					emit(Event{Kind: EventDelta, Field: d.Field, Text: d.Text})
				}
				if extractor.Err() != nil {
					// Show everything so far as raw text; the deltas just sent
					// are part of it, so the UI replaces its field view.
					emit(Event{Kind: EventRaw, Text: raw.String()})
				}
			})
			last = resp
			return resp.Text, err
		},
		func(text string) (content.Content, []Problem) {
			parsed, err := content.Parse([]byte(text))
			if err == nil {
				err = content.Validate(parsed)
			}
			if err != nil {
				return content.Content{}, problemsOf(err)
			}
			return parsed, nil
		})
	if err != nil {
		return ContentResult{}, err
	}
	return ContentResult{
		Content: result, JSON: last.Text, Attempts: attempts,
		Model: last.ServedBy, FallbackUsed: last.FallbackUsed,
	}, nil
}

// failure is a rejected answer on its way back to the model.
type failure struct {
	output   string
	problems []string
}

// repair runs the loop shared by both tasks: ask, check, and on a problem ask
// again with the problems, up to MaxRepairs more times. ask returns the model's
// text or an error that is passed on without repair; check returns the parsed
// value, or the problems that make the text unacceptable.
func repair[T any](
	ctx context.Context,
	stage string,
	rejected func(stage string, attempt int, problems []Problem),
	ask func(ctx context.Context, prev *failure) (string, error),
	check func(text string) (T, []Problem),
) (T, int, error) {
	var zero T
	var prev *failure
	for attempt := 1; attempt <= MaxRepairs+1; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, attempt - 1, fmt.Errorf("%s: %w", stage, err)
		}
		text, err := ask(ctx, prev)
		if err != nil {
			if prev != nil {
				// The model had already given an answer that failed validation; say so, or the caller sees only
				// the transport error and loses the reason a repair was needed.
				return zero, attempt, fmt.Errorf("%s: attempt %d (repairing: %s): %w", stage, attempt, strings.Join(prev.problems, "; "), err)
			}
			return zero, attempt, fmt.Errorf("%s: attempt %d: %w", stage, attempt, err)
		}
		value, problems := check(text)
		if len(problems) == 0 {
			return value, attempt, nil
		}
		rejected(stage, attempt, problems)
		prev = &failure{output: text, problems: problemLines(problems)}
	}
	return zero, MaxRepairs + 1, &GenerationError{
		Stage: stage, Attempts: MaxRepairs + 1, Problems: prev.problems, LastOutput: prev.output,
	}
}

// problemsOf lists what is wrong, one problem per entry. A syntax error has no
// path and is described as a whole.
func problemsOf(err error) []Problem {
	if v, ok := errors.AsType[segment.ValidationErrors](err); ok {
		out := make([]Problem, len(v))
		for i, e := range v {
			out[i] = Problem{Path: e.Path, Message: e.Msg}
		}
		return out
	}
	if v, ok := errors.AsType[content.ValidationErrors](err); ok {
		out := make([]Problem, len(v))
		for i, e := range v {
			out[i] = Problem{Path: e.Path, Code: e.Code, Message: e.Msg}
		}
		return out
	}
	return []Problem{{Message: err.Error()}}
}

// problemLines is the "path: message" list the repair prompt and GenerationError carry.
func problemLines(ps []Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}
