package campaign

import (
	"errors"
	"fmt"
)

// State is the status of a campaign (docs/PLAN.md section 1):
//
//	draft -> audience_ready -> content_ready -> approved -> sending -> completed | completed_with_failures
//
// approved is only ever visible inside the approval transaction: the same transaction writes the recipients and
// moves the campaign on to sending (or straight to completed when nobody is eligible). sending and the two final
// states are moved by the queue, not by the actions below.
type State string

// The states, as stored in campaigns.status.
const (
	StateDraft                 State = "draft"
	StateAudienceReady         State = "audience_ready"
	StateContentReady          State = "content_ready"
	StateApproved              State = "approved"
	StateSending               State = "sending"
	StateCompleted             State = "completed"
	StateCompletedWithFailures State = "completed_with_failures"
)

// Action is something a marketer (or the generation behind it) does to a campaign.
type Action string

// The actions the API performs.
const (
	// ActGenerateSegment asks the model for the segment again (or for the first time).
	ActGenerateSegment Action = "generate_segment"
	// ActSetChannels turns channels on or off.
	ActSetChannels Action = "set_channels"
	// ActSaveContent stores generated or edited content.
	ActSaveContent Action = "save_content"
	// ActApprove freezes the campaign and queues its recipients.
	ActApprove Action = "approve"
)

// ErrInvalidTransition means the action is not allowed in the campaign's state. Errors from Next are *TransitionError
// and match it with errors.Is.
var ErrInvalidTransition = errors.New("the campaign is in a state that does not allow this")

// TransitionError says which action was refused in which state.
type TransitionError struct {
	From   State
	Action Action
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s is not allowed while the campaign is %s", e.Action, e.From)
}

func (e *TransitionError) Is(target error) bool { return target == ErrInvalidTransition }

// transitions is the whole state machine: for each action, the states it may start from and where it leads.
// Everything not listed is refused, which is what makes "edit after approval" a 409 without a special case.
var transitions = map[Action]map[State]State{
	// The content survives a new segment: a campaign with content stays content_ready (the marketer decides what to do
	// with the old text, and the version moves so the approval must name the new one).
	ActGenerateSegment: {StateDraft: StateAudienceReady, StateAudienceReady: StateAudienceReady, StateContentReady: StateContentReady},
	ActSetChannels:     {StateAudienceReady: StateAudienceReady, StateContentReady: StateContentReady},
	ActSaveContent:     {StateAudienceReady: StateContentReady, StateContentReady: StateContentReady},
	ActApprove:         {StateContentReady: StateApproved},
}

// Next returns the state the campaign moves to when the action is performed in state from, or a *TransitionError. It is
// pure; the database update that follows it is conditional on the state it was computed from.
func Next(from State, a Action) (State, error) {
	if to, ok := transitions[a][from]; ok {
		return to, nil
	}
	return "", &TransitionError{From: from, Action: a}
}

// Editable reports whether the campaign can still be edited (its segment, channels or content changed).
func (s State) Editable() bool {
	return s == StateDraft || s == StateAudienceReady || s == StateContentReady
}
