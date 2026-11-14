package campaign

import (
	"errors"
	"testing"
)

// Every combination of state and action, so a new state or action cannot slip in without a decision.
func TestNext(t *testing.T) {
	t.Parallel()
	states := []State{StateDraft, StateAudienceReady, StateContentReady, StateApproved, StateSending, StateCompleted, StateCompletedWithFailures}
	actions := []Action{ActGenerateSegment, ActSetChannels, ActSaveContent, ActApprove}
	const refused = State("")
	want := map[Action]map[State]State{
		ActGenerateSegment: {
			StateDraft: StateAudienceReady, StateAudienceReady: StateAudienceReady, StateContentReady: StateContentReady,
			StateApproved: refused, StateSending: refused, StateCompleted: refused, StateCompletedWithFailures: refused,
		},
		ActSetChannels: {
			StateDraft: refused, StateAudienceReady: StateAudienceReady, StateContentReady: StateContentReady,
			StateApproved: refused, StateSending: refused, StateCompleted: refused, StateCompletedWithFailures: refused,
		},
		ActSaveContent: {
			StateDraft: refused, StateAudienceReady: StateContentReady, StateContentReady: StateContentReady,
			StateApproved: refused, StateSending: refused, StateCompleted: refused, StateCompletedWithFailures: refused,
		},
		ActApprove: {
			StateDraft: refused, StateAudienceReady: refused, StateContentReady: StateApproved,
			StateApproved: refused, StateSending: refused, StateCompleted: refused, StateCompletedWithFailures: refused,
		},
	}
	for _, a := range actions {
		for _, from := range states {
			t.Run(string(a)+" from "+string(from), func(t *testing.T) {
				t.Parallel()
				got, err := Next(from, a)
				if w := want[a][from]; w == refused {
					var te *TransitionError
					if err == nil || !errors.Is(err, ErrInvalidTransition) || !errors.As(err, &te) || te.From != from || te.Action != a {
						t.Fatalf("Next(%s, %s) = %q, %v; want a *TransitionError naming both", from, a, got, err)
					}
				} else if err != nil || got != w {
					t.Fatalf("Next(%s, %s) = %q, %v; want %q", from, a, got, err, w)
				}
			})
		}
	}
	if len(want) != len(actions) {
		t.Fatalf("the table covers %d actions, the test lists %d", len(want), len(actions))
	}
}

func TestNext_everyActionAndStateIsInTheTable(t *testing.T) {
	t.Parallel()
	for a, m := range transitions {
		for from, to := range m {
			if to == "" || from == "" {
				t.Errorf("%s: empty state in %q -> %q", a, from, to)
			}
		}
	}
}

func TestState_Editable(t *testing.T) {
	t.Parallel()
	for s, want := range map[State]bool{
		StateDraft: true, StateAudienceReady: true, StateContentReady: true,
		StateApproved: false, StateSending: false, StateCompleted: false, StateCompletedWithFailures: false,
	} {
		if got := s.Editable(); got != want {
			t.Errorf("%s.Editable() = %v, want %v", s, got, want)
		}
	}
}

func TestTransitionError_message(t *testing.T) {
	t.Parallel()
	_, err := Next(StateSending, ActSaveContent)
	if err == nil || err.Error() != "save_content is not allowed while the campaign is sending" {
		t.Fatalf("error = %v", err)
	}
}
