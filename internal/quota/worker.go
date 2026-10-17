package quota

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/47vigen/remora/internal/accounts"
	"github.com/47vigen/remora/internal/engine/cliproxyapi"
	"github.com/47vigen/remora/internal/store"
)

// ErrRefreshTarget is returned by a refresh that names neither or both of an account and a provider.
var ErrRefreshTarget = errors.New("name one account or one provider")

// Config tunes the Service.
type Config struct {
	Interval time.Duration // 0 = no background polling
	BaseURL  string        // "" = the providers' hosts
}

// maxIdle bounds a round's sleep, so accounts added meanwhile are seen.
const maxIdle = time.Minute

// Service polls subscription accounts' quota, one upstream call at a time.
type Service struct {
	eng Engine
	st  *store.Store
	cfg Config
	log *slog.Logger
	f   *fetcher

	wake      chan struct{}
	mu        sync.Mutex
	queue     []int64             // on-demand checks, in order
	failures  map[int64]int       // consecutive 429/5xx per account
	requested map[int64]time.Time // last accepted on-demand refresh, so a check in flight still holds the floor
}

// spaced keeps at least Spacing between upstream calls, including the calls inside one check.
type spaced struct {
	Engine
	mu   sync.Mutex
	last time.Time
}

func (s *spaced) APICall(ctx context.Context, in cliproxyapi.APICallRequest) (cliproxyapi.APICallResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if wait := time.Until(s.last.Add(Spacing)); wait > 0 {
		select {
		case <-ctx.Done():
			return cliproxyapi.APICallResponse{}, ctx.Err()
		case <-time.After(wait):
		}
	}
	s.last = time.Now()
	return s.Engine.APICall(ctx, in)
}

// New returns a Service. A nil log discards.
func New(eng Engine, st *store.Store, cfg Config, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		eng: eng, st: st, cfg: cfg, log: log,
		f: &fetcher{
			eng: &spaced{Engine: eng}, base: cfg.BaseURL, plans: map[int64]time.Time{},
			ver: newVersionCache(&http.Client{}, upstream(cfg.BaseURL, antigravityManifestURL)),
		},
		wake: make(chan struct{}, 1), failures: map[int64]int{}, requested: map[int64]time.Time{},
	}
}

// Run polls until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		wait := s.round(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(wait):
		}
	}
}

// round checks at most one account and returns how long to sleep before the next round.
func (s *Service) round(ctx context.Context) time.Duration {
	rows, err := s.st.Accounts(ctx)
	if err != nil {
		s.log.Warn("quota: read accounts", "err", err)
		return Spacing
	}
	creds, err := s.eng.Credentials(ctx)
	if err != nil {
		s.log.Debug("quota: read credentials", "err", err)
		return Spacing
	}
	snaps, err := s.st.AccountQuotas(ctx)
	if err != nil {
		s.log.Warn("quota: read snapshots", "err", err)
		return Spacing
	}
	byName := make(map[string]*cliproxyapi.Credential, len(creds))
	for i := range creds {
		byName[creds[i].Name] = &creds[i]
	}
	snap := func(id int64) *store.AccountQuota {
		if q, ok := snaps[id]; ok {
			return &q
		}
		return nil
	}

	now := time.Now()
	var pick *store.Account
	var popped []int64
	defer func() { // a finished or dropped request no longer holds the floor; ids are reused after a delete
		s.mu.Lock()
		for _, id := range popped {
			delete(s.requested, id)
		}
		s.mu.Unlock()
	}()
	for pick == nil {
		id, ok := s.popQueue()
		if !ok {
			break
		}
		popped = append(popped, id)
		if i := slices.IndexFunc(rows, func(a store.Account) bool { return a.ID == id }); i >= 0 && rows[i].Enabled &&
			byName[rows[i].Credential] != nil && !byName[rows[i].Credential].Disabled {
			pick = &rows[i]
		}
	}
	next := now.Add(maxIdle)
	if pick == nil {
		s.mu.Lock()
		for i, a := range rows {
			at, ok := nextPoll(a, byName[a.Credential], snap(a.ID), s.failures[a.ID], s.cfg.Interval)
			switch {
			case !ok:
			case !at.After(now) && (pick == nil || at.Before(next)):
				pick, next = &rows[i], at
			case pick == nil && at.Before(next):
				next = at
			}
		}
		s.mu.Unlock()
	}
	if pick == nil {
		return next.Sub(now)
	}
	s.check(ctx, *pick, *byName[pick.Credential], snap(pick.ID))
	return 0
}

func (s *Service) popQueue() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return 0, false
	}
	id := s.queue[0]
	s.queue = s.queue[1:]
	return id, true
}

// check polls one account and stores the outcome over its previous snapshot.
func (s *Service) check(ctx context.Context, a store.Account, c cliproxyapi.Credential, prev *store.AccountQuota) {
	q := store.AccountQuota{AccountID: a.ID, Source: "poll"}
	if prev != nil {
		q = *prev
	}
	got, err := s.f.fetch(ctx, a, c, q)
	var ue *upstreamError
	if errors.As(err, &ue) && (ue.Status == http.StatusUnauthorized || ue.Status == http.StatusForbidden) {
		if rerr := s.eng.RefreshCredential(ctx, c.Name); rerr == nil {
			got, err = s.f.fetch(ctx, a, c, q)
		}
	}
	if ctx.Err() != nil {
		return
	}
	now := time.Now()
	q.CheckedAt = now
	msg, backoff := outcome(err)
	s.mu.Lock()
	switch {
	case err == nil:
		if got.Plan != "" {
			q.Plan = got.Plan
		}
		q.Windows, q.LimitReached, q.Source, q.FetchedAt, q.Error = got.Windows, got.LimitReached, "poll", now, ""
		delete(s.failures, a.ID)
	case backoff:
		q.Error = msg
		s.failures[a.ID]++
	default:
		q.Error = msg
	}
	s.mu.Unlock()
	if err != nil {
		s.log.Debug("quota: check failed", "account", a.ID, "err", err)
	}
	if perr := s.st.PutAccountQuota(ctx, q); perr != nil {
		if errors.Is(perr, store.ErrNotFound) {
			s.forget(a.ID)
			return
		}
		s.log.Warn("quota: store snapshot", "account", a.ID, "err", perr)
	}
}

// outcome maps a check's error to its snapshot message and whether it counts toward the backoff.
func outcome(err error) (string, bool) {
	var ue *upstreamError
	var ee *cliproxyapi.Error
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, errSignIn):
		return MsgSignIn, false
	case errors.Is(err, errSuspended):
		return err.Error(), false
	case errors.Is(err, errUnreadable):
		return MsgUnreadable, false
	case errors.As(err, &ue):
		switch {
		case ue.Status == http.StatusUnauthorized, ue.Status == http.StatusForbidden:
			return MsgSignIn, false
		case ue.Status == http.StatusTooManyRequests:
			return MsgRateLimited, true
		case ue.Status >= 500:
			return MsgUnavailable, true
		}
		return MsgRefused, false
	case errors.As(err, &ee):
		switch ee.StatusCode {
		case http.StatusBadRequest:
			return MsgSignIn, false
		case http.StatusBadGateway:
			return MsgUnavailable, true
		}
	}
	return MsgFailed, false
}

func (s *Service) forget(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.failures, id)
	s.queue = slices.DeleteFunc(s.queue, func(q int64) bool { return q == id })
}

// Refresh queues checks of one account (accountID > 0) or of every enabled account of provider. An account checked
// less than RefreshFloor ago, or disabled, is skipped.
func (s *Service) Refresh(ctx context.Context, accountID int64, provider string) (queued, skipped []int64, err error) {
	if (accountID > 0) == (provider != "") {
		return nil, nil, ErrRefreshTarget
	}
	var targets []store.Account
	if accountID > 0 {
		a, err := s.st.Account(ctx, accountID)
		if err != nil {
			return nil, nil, err
		}
		if !Supported(a.Provider) {
			return nil, nil, ErrUnsupported
		}
		targets = []store.Account{a}
	} else {
		if _, ok := cliproxyapi.ProviderByName(provider); !ok {
			return nil, nil, fmt.Errorf("%w: %q", accounts.ErrUnknownProvider, provider)
		}
		if !Supported(provider) {
			return nil, nil, ErrUnsupported
		}
		rows, err := s.st.Accounts(ctx)
		if err != nil {
			return nil, nil, err
		}
		for _, a := range rows {
			if a.Provider == provider {
				targets = append(targets, a)
			}
		}
	}
	snaps, err := s.st.AccountQuotas(ctx)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	s.mu.Lock()
	for _, a := range targets {
		q, ok := snaps[a.ID]
		if !a.Enabled || (ok && now.Sub(q.CheckedAt) < RefreshFloor) || now.Sub(s.requested[a.ID]) < RefreshFloor {
			skipped = append(skipped, a.ID)
			continue
		}
		s.requested[a.ID] = now // the snapshot only moves once the check finishes
		queued = append(queued, a.ID)
		if !slices.Contains(s.queue, a.ID) {
			s.queue = append(s.queue, a.ID)
		}
	}
	s.mu.Unlock()
	if len(queued) > 0 {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	return queued, skipped, nil
}

// Snapshots returns the stored snapshots, each replaced by the passive one when the engine observed it later.
func (s *Service) Snapshots(ctx context.Context) (map[int64]store.AccountQuota, error) {
	snaps, err := s.st.AccountQuotas(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.st.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	creds, err := s.eng.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range rows {
		i := slices.IndexFunc(creds, func(c cliproxyapi.Credential) bool { return c.Name == a.Credential })
		if !a.Enabled || i < 0 || creds[i].Quota.ObservedAt == nil {
			continue
		}
		observed := *creds[i].Quota.ObservedAt
		stored, has := snaps[a.ID]
		if has && !observed.After(stored.FetchedAt) {
			continue
		}
		p, ok := passive(a.Provider, creds[i].Quota.Signals, observed)
		if !ok {
			continue
		}
		p.AccountID, p.CheckedAt = a.ID, observed
		if has {
			if stored.CheckedAt.After(observed) {
				p.CheckedAt = stored.CheckedAt
			}
			if p.Plan == "" {
				p.Plan = stored.Plan
			}
		}
		snaps[a.ID] = p
	}
	return snaps, nil
}
