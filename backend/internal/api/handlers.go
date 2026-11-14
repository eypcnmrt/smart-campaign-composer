package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/content"
)

func reqID(r *http.Request) string { return middleware.GetReqID(r.Context()) }

// parseID reads the {id} of the route. A malformed id names no campaign, so it is a 404 like any other missing one.
func parseID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errNotFound
	}
	return id, nil
}

// intParam reads an optional integer query parameter within [lo, hi].
func intParam(r *http.Request, name string, def, lo, hi int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, validationProblem(detail{Path: name, Code: codeInvalidParam, Params: map[string]any{"min": lo, "max": hi},
			Message: fmt.Sprintf("must be a whole number from %d to %d", lo, hi)})
	}
	return n, nil
}

// boolParam reads an optional true/false query parameter (absent is false).
func boolParam(r *http.Request, name string) (bool, error) {
	switch raw := r.URL.Query().Get(name); raw {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, validationProblem(detail{Path: name, Code: codeInvalidParam, Message: "must be true or false"})
	}
}

func (s *Server) loadCampaign(r *http.Request) (campaign.Campaign, error) {
	id, err := parseID(r)
	if err != nil {
		return campaign.Campaign{}, err
	}
	return s.store.Get(r.Context(), id)
}

// POST /api/campaigns
func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Brief string `json:"brief"`
	}
	if err := s.decode(w, r, &req); err != nil {
		return err
	}
	brief := strings.TrimSpace(req.Brief)
	switch n := utf8.RuneCountInString(brief); {
	case n == 0:
		return validationProblem(detail{Path: "brief", Code: content.CodeEmpty, Message: "must not be empty"})
	case n > s.cfg.MaxBriefChars:
		return validationProblem(detail{Path: "brief", Code: content.CodeTooLong, Params: map[string]any{"length": n, "limit": s.cfg.MaxBriefChars},
			Message: fmt.Sprintf("is %d characters long; the limit is %d", n, s.cfg.MaxBriefChars)})
	}

	// The slot first: a busy server must not leave an empty draft behind for every refused request.
	release, err := s.gate.acquire(r.Context())
	if err != nil {
		return err
	}
	defer release()

	ctx := r.Context()
	id, err := s.store.Create(ctx, brief)
	if err != nil {
		return err
	}
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	c, err = s.runSegment(ctx, c, reqID(r))
	if err != nil {
		// The draft stays: the marketer can try again with POST /api/campaigns/{id}/segment.
		p := s.problemOf(err, reqID(r))
		q := *p
		q.Details = withCampaignID(p.Details, id)
		return &q
	}
	writeJSON(w, http.StatusCreated, c)
	return nil
}

// withCampaignID adds the id of the draft that a failed generation left behind to a problem's details.
func withCampaignID(details any, id int64) map[string]any {
	out := map[string]any{}
	if m, ok := details.(map[string]any); ok {
		for k, v := range m {
			out[k] = v
		}
	}
	out["campaign_id"] = id
	return out
}

// runSegment asks the model for the segment of c's brief, counts the audience and stores both. The caller holds a
// generation slot.
//
// The generation ends when the client goes away (ctx), when the server starts shutting down (BeginShutdown, the same signal
// the content stream listens to: the request then ends with server_shutting_down while http.Server.Shutdown is still
// waiting for it, instead of outliving the grace period and getting a cut connection) or when it takes too long.
func (s *Server) runSegment(ctx context.Context, c campaign.Campaign, id string) (campaign.Campaign, error) {
	if s.shutdownCtx.Err() != nil {
		return campaign.Campaign{}, errShuttingDown // the model is not asked, no credit is spent
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := context.AfterFunc(s.shutdownCtx, func() { cancel(errShuttingDown) })
	defer stop()
	ctx, cancelTimeout := context.WithTimeoutCause(ctx, s.cfg.SegmentTimeout, errTaskTimeout)
	defer cancelTimeout()
	start := time.Now()

	res, err := s.gen.Segment(ctx, c.Brief)
	if err != nil {
		// A context error that the model layer wrapped is reported by what stopped the generation (shutdown, deadline).
		err = cause(ctx, taskTimeout(err))
		s.metrics.ObserveLLM("segment", outcomeOf(err), time.Since(start))
		return campaign.Campaign{}, err
	}
	s.metrics.ObserveLLM("segment", "ok", time.Since(start))

	// The model's time is spent; counting and saving get a deadline of their own, so a generation that ended late is not
	// lost to the budget that was meant for the model.
	dbCtx, cancelDB := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancelDB()
	aud, err := s.store.CountAudience(dbCtx, res.Rule)
	if err != nil {
		return campaign.Campaign{}, err
	}
	return s.store.SetSegment(dbCtx, c, res.JSON, res.Rule, aud)
}

// GET /api/campaigns
func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) error {
	limit, err := intParam(r, "limit", 50, 1, 200)
	if err != nil {
		return err
	}
	list, err := s.store.List(r.Context(), limit)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": list})
	return nil
}

// GET /api/campaigns/{id}
func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) error {
	c, err := s.loadCampaign(r)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, c)
	return nil
}

// POST /api/campaigns/{id}/segment
func (s *Server) regenerateSegment(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	// The lock comes before the campaign is read: what is checked below must still be true when the model is asked, and a
	// generation that finished a moment ago has already changed it.
	unlock, ok := s.locks.tryLock(id)
	if !ok {
		return errGenerationInProgress
	}
	defer unlock()
	c, err := s.store.Get(r.Context(), id)
	if err != nil {
		return err
	}
	// Refuse before the model is asked: a campaign that cannot take a new segment must not cost a call.
	if _, err := campaign.Next(c.Status, campaign.ActGenerateSegment); err != nil {
		return err
	}
	release, err := s.gate.acquire(r.Context())
	if err != nil {
		return err
	}
	defer release()

	c, err = s.runSegment(r.Context(), c, reqID(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, c)
	return nil
}

var errGenerationInProgress = &problem{Status: http.StatusConflict, Code: "generation_in_progress",
	Message: "This campaign is already being generated. Wait for it to finish."}

// PUT /api/campaigns/{id}/channels
func (s *Server) setChannels(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Channels []string `json:"channels"`
	}
	if err := s.decode(w, r, &req); err != nil {
		return err
	}
	c, err := s.loadCampaign(r)
	if err != nil {
		return err
	}
	if _, err := campaign.Next(c.Status, campaign.ActSetChannels); err != nil {
		return err // the state is checked before the values, so an edit after approval is a 409 whatever it says
	}
	c, err = s.store.SetChannels(r.Context(), c, req.Channels)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, c)
	return nil
}

// PUT /api/campaigns/{id}/content
func (s *Server) putContent(w http.ResponseWriter, r *http.Request) error {
	var req content.Content
	if err := s.decode(w, r, &req); err != nil {
		return err
	}
	c, err := s.loadCampaign(r)
	if err != nil {
		return err
	}
	if _, err := campaign.Next(c.Status, campaign.ActSaveContent); err != nil {
		return err
	}
	// The same validator as for generated text: limits, variables, characters. Every problem comes back with its field.
	if err := content.Validate(req); err != nil {
		return err
	}
	c, err = s.store.SaveContent(r.Context(), c, req, campaign.OriginEdited)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, c)
	return nil
}

// GET /api/campaigns/{id}/preview
func (s *Server) preview(w http.ResponseWriter, r *http.Request) error {
	limit, err := intParam(r, "limit", 3, 1, 10)
	if err != nil {
		return err
	}
	c, err := s.loadCampaign(r)
	if err != nil {
		return err
	}
	samples, err := s.store.Preview(r.Context(), c, limit)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": samples})
	return nil
}

// POST /api/campaigns/{id}/approve
func (s *Server) approve(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		// Version is the version the marketer reviewed; a pointer, so a missing one can be told from version 0.
		Version             *int `json:"version"`
		AcknowledgeWarnings bool `json:"acknowledge_warnings"`
	}
	if err := s.decode(w, r, &req); err != nil {
		return err
	}
	id, err := parseID(r)
	if err != nil {
		return err
	}
	if req.Version == nil {
		return validationProblem(detail{Path: "version", Code: codeRequired, Message: "is required: the version of the campaign that was reviewed"})
	}
	res, err := s.store.Approve(r.Context(), id, *req.Version, req.AcknowledgeWarnings)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": res.Campaign, "enqueued": res.Enqueued})
	return nil
}

// GET /api/campaigns/{id}/stats
func (s *Server) stats(w http.ResponseWriter, r *http.Request) error {
	id, err := parseID(r)
	if err != nil {
		return err
	}
	st, err := s.store.Stats(r.Context(), s.queue, id, s.cfg.WorkerStaleAfter)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}
