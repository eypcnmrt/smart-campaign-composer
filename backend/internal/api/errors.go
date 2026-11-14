package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
)

// problem is an error with its HTTP answer. Every error response of the API has the same body:
//
//	{"error": {"code": "...", "message": "...", "details": ...}}
//
// code is stable and meant for programs (the UI maps it to its own wording), message is for people and may change, and
// details carries the specifics of the code (field paths, the current version, the warnings) or null.
type problem struct {
	Status  int
	Code    string
	Message string
	Details any
	// RetryAfter, when positive, is sent as the Retry-After header.
	RetryAfter time.Duration
}

func (p *problem) Error() string { return p.Code + ": " + p.Message }

// errorPayload and errorBody are the JSON shape of the error response.
type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

type errorBody struct {
	Error errorPayload `json:"error"`
}

func (p *problem) body() errorBody {
	return errorBody{Error: errorPayload{Code: p.Code, Message: p.Message, Details: p.Details}}
}

// detail names one invalid field. Code and Params are what a client translates (message is English text for people and
// logs, and may change); Params holds the values the sentence needs (a limit, a length, a name).
type detail struct {
	Path    string         `json:"path"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}

// Codes of the fields the handlers check themselves; the others come from content and campaign.
const (
	codeInvalidParam = "invalid_param" // min and max for a number, none for a flag
	codeRequired     = "required"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	// Encode before anything is sent: a value that cannot be encoded must become a 500, not a 200 with an empty body.
	body, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		body, _ = json.Marshal((&problem{Code: "internal", Message: "The response could not be encoded."}).body())
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// A failed write here is a broken connection: there is nobody to tell.
	_, _ = w.Write(append(body, '\n'))
}

// taskTimeout names a generation that ran into its own deadline, so it is not confused with a database timeout.
func taskTimeout(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", errTaskTimeout, err)
	}
	return err
}

func writeProblem(w http.ResponseWriter, p *problem) {
	if p.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(p.RetryAfter.Seconds()))))
	}
	writeJSON(w, p.Status, p.body())
}

func validationProblem(details ...detail) *problem {
	return &problem{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "The request has invalid fields.", Details: details}
}

var errNotFound = &problem{Status: http.StatusNotFound, Code: "not_found", Message: "There is no such campaign."}

// Stable error codes of the generation and of the stream that the server decides itself.
var (
	errShuttingDown = errors.New("the server is shutting down")
	errSlowClient   = errors.New("the client does not read the stream fast enough")
	errTaskTimeout  = errors.New("the generation took too long")
)

// problemOf turns an error into its answer. Errors it does not know are internal: they are logged with the request id
// and the client gets only the id, never the text, which may contain SQL or provider details.
func (s *Server) problemOf(err error, reqID string) *problem {
	if p := knownProblem(err); p != nil {
		// The client gets a generic answer for the failures that are the operator's to fix (a wrong key or model name,
		// an unavailable provider, a full queue), so the cause must be in the log or it exists nowhere.
		if p.Status >= http.StatusInternalServerError || strings.HasPrefix(p.Code, "llm_") {
			s.log.Warn("request failed", "request_id", reqID, "code", p.Code, "status", p.Status, "err", err)
		}
		return p
	}
	s.log.Error("internal error", "request_id", reqID, "err", err)
	return &problem{Status: http.StatusInternalServerError, Code: "internal", Message: "Something went wrong on the server.", Details: map[string]any{"request_id": reqID}}
}

// knownProblem maps the errors the API expects; nil for anything else.
func knownProblem(err error) *problem {
	var p *problem
	if errors.As(err, &p) {
		return p
	}
	if p := llmProblem(err); p != nil {
		return p
	}
	return storeProblem(err)
}

// storeProblem maps the errors of the campaign store (and the validators behind it); nil when err is none of them.
func storeProblem(err error) *problem {
	switch {
	case errors.Is(err, campaign.ErrNotFound):
		return errNotFound
	case errors.Is(err, campaign.ErrInvalidTransition):
		p := &problem{Status: http.StatusConflict, Code: "invalid_state", Message: "The campaign is in a state that does not allow this."}
		if te, ok := errors.AsType[*campaign.TransitionError](err); ok {
			p.Message = fmt.Sprintf("The campaign is %s; %s is not possible now.", te.From, strings.ReplaceAll(string(te.Action), "_", " "))
			p.Details = map[string]any{"status": te.From, "action": te.Action}
		}
		return p
	case errors.Is(err, campaign.ErrApprovalUnconfirmed):
		return &problem{Status: http.StatusInternalServerError, Code: "approval_unconfirmed",
			Message: "The approval may have been applied. Reload the campaign and check its status before approving again."}
	case errors.Is(err, campaign.ErrStateChanged):
		return &problem{Status: http.StatusConflict, Code: "state_changed", Message: "The campaign was changed while this request was being handled. Reload it and try again."}
	case errors.Is(err, campaign.ErrVersionMismatch):
		p := &problem{Status: http.StatusConflict, Code: "version_mismatch", Message: "The campaign is not at the version that was reviewed. Reload it, review it again and approve that version."}
		if vm, ok := errors.AsType[*campaign.VersionMismatchError](err); ok {
			p.Details = map[string]any{"current_version": vm.Current}
		}
		return p
	case errors.Is(err, campaign.ErrNoRecipients):
		p := &problem{Status: http.StatusUnprocessableEntity, Code: "no_recipients",
			Message: "Nobody in the audience has consented to the selected channels, so there is nothing to send. " +
				"Select another channel or change the brief; acknowledging warnings does not apply."}
		if nr, ok := errors.AsType[*campaign.NoRecipientsError](err); ok {
			p.Details = map[string]any{"channels": nr.Channels, "audience": nr.Audience}
		}
		return p
	case errors.Is(err, campaign.ErrAcknowledgementRequired):
		p := &problem{Status: http.StatusUnprocessableEntity, Code: "acknowledgement_required", Message: "The campaign has warnings. Approve it with acknowledge_warnings set to true to confirm them."}
		if ack, ok := errors.AsType[*campaign.AcknowledgementRequiredError](err); ok {
			p.Details = map[string]any{"warnings": ack.Warnings}
		}
		return p
	}
	if v, ok := errors.AsType[campaign.ValidationErrors](err); ok {
		details := make([]detail, len(v))
		for i, e := range v {
			details[i] = detail{Path: e.Path, Code: e.Code, Message: e.Msg, Params: e.Params}
		}
		return validationProblem(details...)
	}
	if v, ok := errors.AsType[content.ValidationErrors](err); ok {
		details := make([]detail, len(v))
		for i, e := range v {
			details[i] = detail{Path: e.Path, Code: e.Code, Message: e.Msg, Params: e.Params}
		}
		return validationProblem(details...)
	}
	return nil
}

// llmProblem maps the errors of a generation; nil when err is none of them. The details never contain the provider's own
// text (it can echo the prompt), only what the client can act on.
func llmProblem(err error) *problem {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, errShuttingDown):
		return &problem{Status: http.StatusServiceUnavailable, Code: "server_shutting_down", Message: "The server is shutting down. Try again in a moment."}
	case errors.Is(err, errSlowClient):
		return &problem{Status: http.StatusServiceUnavailable, Code: "client_too_slow", Message: "The stream was closed because it was not read fast enough."}
	case errors.Is(err, errBusy):
		return &problem{Status: http.StatusServiceUnavailable, Code: "llm_busy", Message: "Too many generations are running. Try again shortly.", RetryAfter: busyRetryAfter}
	case errors.Is(err, llm.ErrRefusal):
		p := &problem{Status: http.StatusUnprocessableEntity, Code: "llm_refused", Message: "The model declined to work on this brief."}
		if re, ok := errors.AsType[*llm.RefusalError](err); ok && re.Category != "" {
			p.Details = map[string]any{"category": re.Category}
		}
		return p
	case errors.Is(err, llm.ErrRateLimited):
		p := &problem{Status: http.StatusTooManyRequests, Code: "llm_rate_limited", Message: "The model provider is rate limiting requests. Try again later."}
		if rl, ok := errors.AsType[*llm.RateLimitError](err); ok && rl.RetryAfter > 0 {
			p.RetryAfter = rl.RetryAfter
			p.Details = map[string]any{"retry_after_seconds": int(math.Ceil(rl.RetryAfter.Seconds()))}
		}
		return p
	case errors.Is(err, compose.ErrRepairExhausted):
		p := &problem{Status: http.StatusBadGateway, Code: "llm_invalid_output", Message: "The model's answer was still invalid after it was asked to correct it."}
		if ge, ok := errors.AsType[*compose.GenerationError](err); ok {
			p.Details = map[string]any{"stage": ge.Stage, "attempts": ge.Attempts, "problems": ge.Problems}
		}
		return p
	case errors.Is(err, llm.ErrMaxTokens):
		return &problem{Status: http.StatusBadGateway, Code: "llm_output_truncated", Message: "The model's answer was cut off. Try a shorter brief."}
	case errors.Is(err, llm.ErrTimeout), errors.Is(err, errTaskTimeout):
		// A bare context.DeadlineExceeded is not matched on purpose: a database call that ran into its own deadline is not
		// a slow model. The generation turns its own deadline into errTaskTimeout (see taskTimeout).
		return &problem{Status: http.StatusGatewayTimeout, Code: "llm_timeout", Message: "The model took too long to answer. Try again."}
	case errors.Is(err, llm.ErrUnavailable):
		return &problem{Status: http.StatusServiceUnavailable, Code: "llm_unavailable", Message: "The model provider is not available. Try again later."}
	case errors.Is(err, llm.ErrBadRequest), errors.Is(err, llm.ErrUnexpectedStop):
		// Usually a wrong model name or key: for the operator, not for the marketer.
		return &problem{Status: http.StatusBadGateway, Code: "llm_error", Message: "The model provider could not handle the request."}
	case errors.Is(err, context.Canceled):
		return &problem{Status: 499, Code: "client_closed", Message: "The request was canceled."}
	}
	return nil
}

// outcomeOf is the metrics label of a generation: ok, or the error code without its llm_ prefix.
func outcomeOf(err error) string {
	if err == nil {
		return "ok"
	}
	if p := knownProblem(err); p != nil {
		return strings.TrimPrefix(p.Code, "llm_")
	}
	return "internal"
}
